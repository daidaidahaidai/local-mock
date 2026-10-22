package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	mock "github.com/daidaidahaidai/local-mock/internal/mocking/domain"
	"github.com/daidaidahaidai/local-mock/internal/mocking/infrastructure/grpcmock"
	agent "github.com/daidaidahaidai/local-mock/internal/orchestration/application"
	"github.com/daidaidahaidai/local-mock/internal/orchestration/domain"
	"github.com/daidaidahaidai/local-mock/internal/orchestration/infrastructure/repository"
	scenarioapp "github.com/daidaidahaidai/local-mock/internal/scenario/application"
	scenario "github.com/daidaidahaidai/local-mock/internal/scenario/domain"
	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

type Project struct {
	Doc       loader.Document
	Seed      scenario.Scenario
	Repo      *repository.Workspace
	Dir       string
	schemas   map[string]any
	contracts map[string]protoreflect.ServiceDescriptor
}

func New(ctx context.Context, repo, seed, dir string) (*Project, error) {
	workspace, err := repository.New(repo)
	if err != nil {
		return nil, err
	}
	doc, err := loader.LoadContext(ctx, seed)
	if err != nil {
		return nil, err
	}
	if _, err = scenarioapp.Resolve(doc.Scenario, doc.Services, doc.Files); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	p := &Project{Doc: doc, Seed: doc.Scenario, Repo: workspace, Dir: absolute, schemas: map[string]any{}, contracts: map[string]protoreflect.ServiceDescriptor{}}
	for name, svc := range doc.Services {
		for _, impl := range svc.Implementations {
			for key := range impl.Env {
				if secretKey(key) {
					return nil, fmt.Errorf("catalog contains credential-like environment key %s; remove it before model analysis", key)
				}
			}
		}
		for iface, contract := range svc.Provides {
			if contract.Protocol != "grpc" {
				continue
			}
			descriptors, _, e := grpcmock.Compile(ctx, contract.Contract)
			if e != nil {
				return nil, e
			}
			service, e := grpcmock.Service(descriptors, contract.Service)
			if e != nil {
				return nil, e
			}
			p.contracts[name+"."+iface] = service
			methods := map[string]any{}
			for i := 0; i < service.Methods().Len(); i++ {
				m := service.Methods().Get(i)
				methods[string(m.Name())] = map[string]any{"request": schema(m.Input(), 0), "response": schema(m.Output(), 0), "streaming": m.IsStreamingClient() || m.IsStreamingServer()}
			}
			p.schemas[name+"."+iface] = methods
		}
	}
	for _, d := range doc.Scenario.Deployments {
		for key := range d.Env {
			if secretKey(key) {
				return nil, fmt.Errorf("scenario contains credential-like key %s", key)
			}
		}
	}
	// Bound each role's input; never ship binary descriptor sets or an unbounded catalog to the model.
	contextJSON, _ := json.Marshal(p.Context("runtime"))
	if len(contextJSON) > 128<<10 {
		return nil, fmt.Errorf("catalog context exceeds 128 KiB; narrow the seed catalog")
	}
	if err = os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	if err = os.Mkdir(absolute, 0700); err != nil {
		return nil, fmt.Errorf("output must be a new directory: %w", err)
	}
	return p, nil
}
func (p *Project) RepositoryTools() []agent.Tool              { return p.Repo.Tools() }
func (p *Project) ValidateRepository(b json.RawMessage) error { return p.Repo.Validate(b) }
func (p *Project) Context(role string) any {
	selected := map[string]map[string]bool{}
	for _, d := range p.Doc.Scenario.Deployments {
		if selected[d.Service] == nil {
			selected[d.Service] = map[string]bool{}
		}
		selected[d.Service][d.Implementation] = true
	}
	catalog := map[string]any{}
	schemas := map[string]any{}
	hints := []string{}
	for name, svc := range p.Doc.Services {
		implementations := map[string]any{}
		for n, i := range svc.Implementations {
			if role != "repository" && role != "topology" && !selected[name][n] {
				continue
			}
			v := map[string]any{"type": i.Type, "role": i.Role, "endpoints": i.Endpoints, "dependencies": i.Dependencies}
			if role == "repository" {
				v = map[string]any{"type": i.Type, "build": i.Build}
				if i.Build != nil {
					rel, err := filepath.Rel(p.Repo.Root, filepath.Join(i.Build.Context, i.Build.Dockerfile))
					if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
						hints = append(hints, filepath.ToSlash(rel))
					}
				}
			}
			if role == "config" || role == "runtime" {
				v["env"] = i.Env
			}
			if (role == "mock" || role == "runtime") && len(i.MockBundle) > 0 {
				var b mock.Bundle
				_ = json.Unmarshal(i.MockBundle, &b)
				rules := []domain.Rule{}
				for _, r := range b.Rules {
					rules = append(rules, domain.Rule{Method: r.Method, Response: r.Response, ErrorCode: r.ErrorCode, ErrorMessage: r.ErrorMessage, DelayMS: r.DelayMS})
				}
				v["mock_rules"] = rules
			}
			if role == "mock" && i.Type == "mock" {
				for iface := range i.Endpoints {
					schemas[name+"."+iface] = p.schemas[name+"."+iface]
				}
			}
			implementations[n] = v
		}
		if len(implementations) > 0 {
			catalog[name] = map[string]any{"provides": svc.Provides, "implementations": implementations}
		}
	}
	if role == "repository" {
		return map[string]any{"repository_root": p.Repo.Root, "catalog": catalog, "inspection_hints": hints}
	}
	if role == "runtime" {
		for _, d := range p.Doc.Scenario.Deployments {
			for iface := range p.Doc.Services[d.Service].Implementations[d.Implementation].Endpoints {
				if s, ok := p.schemas[d.Service+"."+iface]; ok {
					schemas[d.Service+"."+iface] = s
				}
			}
		}
	}
	if role == "debug" {
		return map[string]any{"selected": topologyOf(p.Doc.Scenario)}
	}
	result := map[string]any{"catalog": catalog, "selected": topologyOf(p.Doc.Scenario)}
	components := map[string]any{}
	for name, c := range p.Seed.Components {
		components[name] = map[string]any{"roles": c.Roles, "endpoints": c.Endpoints, "image": c.Image}
	}
	result["user_selected_components_read_only"] = components
	if role == "topology" {
		result["seed"] = topologyOf(p.Seed)
	}
	if role == "config" {
		result["seed_env"] = envOf(p.Seed)
	}
	if role == "mock" || role == "runtime" {
		result["grpc_methods"] = schemas
		result["effective_overrides"] = envOf(p.Doc.Scenario)
	}
	return result
}

func topologyOf(s scenario.Scenario) domain.Topology {
	t := domain.Topology{Name: s.Name, Deployments: map[string]domain.Deployment{}, Bindings: s.Bindings}
	for n, d := range s.Deployments {
		t.Deployments[n] = domain.Deployment{Service: d.Service, Implementation: d.Implementation, Publish: d.Publish}
	}
	return t
}
func envOf(s scenario.Scenario) map[string]map[string]string {
	out := map[string]map[string]string{}
	for n, d := range s.Deployments {
		out[n] = d.Env
	}
	return out
}
func toScenario(t domain.Topology) scenario.Scenario {
	s := scenario.Scenario{Name: t.Name, Bindings: t.Bindings, Deployments: map[string]scenario.Deployment{}}
	for n, d := range t.Deployments {
		s.Deployments[n] = scenario.Deployment{Service: d.Service, Implementation: d.Implementation, Publish: d.Publish}
	}
	return s
}
func (p *Project) CheckTopology(t domain.Topology) error {
	if t.Reason == "" {
		return fmt.Errorf("topology needs reason")
	}
	if len(t.Deployments) > 32 {
		return fmt.Errorf("at most 32 deployments")
	}
	s := toScenario(t)
	s.Components = p.Seed.Components
	problems := scenario.Validate(s, p.Doc.Services)
	if len(problems) > 0 {
		return problems[0]
	}
	return nil
}
func (p *Project) ApplyTopology(t domain.Topology) {
	p.Doc.Scenario = toScenario(t)
	p.Doc.Scenario.Components = p.Seed.Components
}
func (p *Project) configured(c domain.Config) scenario.Scenario {
	s := toScenario(topologyOf(p.Doc.Scenario))
	s.Components = p.Seed.Components
	for name, d := range s.Deployments {
		d.Env = map[string]string{}
		for key, value := range c.Env[name] {
			d.Env[key] = value
		}
		for key, value := range p.Seed.Deployments[name].Env {
			if strings.Contains(value, "${component.") {
				d.Env[key] = value
			}
		}
		s.Deployments[name] = d
	}
	return s
}
func (p *Project) CheckConfig(c domain.Config) error {
	if c.Reason == "" {
		return fmt.Errorf("config needs reason")
	}
	for name, env := range c.Env {
		if _, ok := p.Doc.Scenario.Deployments[name]; !ok {
			return fmt.Errorf("unknown deployment %s", name)
		}
		for key, value := range env {
			seed := p.Seed.Deployments[name].Env[key]
			if seed == "" {
				d := p.Doc.Scenario.Deployments[name]
				seed = p.Doc.Services[d.Service].Implementations[d.Implementation].Env[key]
			}
			if strings.Contains(seed, "${component.") && value != seed {
				return fmt.Errorf("component binding %s.%s is user-selected and immutable", name, key)
			}
			if secretKey(key) {
				return fmt.Errorf("agent cannot generate credential-like key %s", key)
			}
			if len(value) > 8192 {
				return fmt.Errorf("environment value too large")
			}
		}
	}
	problems := scenario.Validate(p.configured(c), p.Doc.Services)
	if len(problems) > 0 {
		return problems[0]
	}
	return nil
}
func (p *Project) ApplyConfig(c domain.Config) { p.Doc.Scenario = p.configured(c) }
func (p *Project) replacement(m domain.Mock) (mock.Bundle, error) {
	var b mock.Bundle
	selected := false
	for _, d := range p.Doc.Scenario.Deployments {
		if d.Service == m.Service && d.Implementation == m.Implementation {
			selected = true
		}
	}
	impl, ok := p.Doc.Services[m.Service].Implementations[m.Implementation]
	if !ok || !selected || impl.Type != "mock" {
		return b, fmt.Errorf("%s/%s is not a selected configured mock", m.Service, m.Implementation)
	}
	if err := json.Unmarshal(impl.MockBundle, &b); err != nil {
		return b, err
	}
	b.Rules = nil
	for _, r := range m.Rules {
		b.Rules = append(b.Rules, mock.Rule{Method: r.Method, Response: r.Response, ErrorCode: canonicalGRPCCode(r.ErrorCode), ErrorMessage: r.ErrorMessage, DelayMS: r.DelayMS})
	}
	_, err := grpcmock.Validate(b)
	return b, err
}
func (p *Project) CheckMocks(m domain.Mocks) error {
	if m.Reason == "" {
		return fmt.Errorf("mock decisions need reason")
	}
	seen := map[string]bool{}
	for _, v := range m.Mocks {
		key := v.Service + "/" + v.Implementation
		if seen[key] {
			return fmt.Errorf("duplicate mock replacement %s", key)
		}
		seen[key] = true
		if _, err := p.replacement(v); err != nil {
			return err
		}
	}
	return nil
}
func (p *Project) ApplyMocks(m domain.Mocks) {
	for _, v := range m.Mocks {
		bundle, _ := p.replacement(v)
		svc := p.Doc.Services[v.Service]
		impl := svc.Implementations[v.Implementation]
		impl.MockBundle, _ = json.Marshal(bundle)
		svc.Implementations[v.Implementation] = impl
		p.Doc.Services[v.Service] = svc
	}
}
func (p *Project) CheckRuntime(r domain.Runtime) error {
	if r.Reason == "" || len(r.Probes) < 1 || len(r.Probes) > 8 {
		return fmt.Errorf("runtime needs reason and 1..8 probes")
	}
	if _, err := scenarioapp.Resolve(p.Doc.Scenario, p.Doc.Services, p.Doc.Files); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, probe := range r.Probes {
		if !scenario.ValidName(probe.Name) || seen[probe.Name] {
			return fmt.Errorf("probe names must be valid and unique")
		}
		seen[probe.Name] = true
		dep, ok := p.Doc.Scenario.Deployments[probe.Deployment]
		if !ok {
			return fmt.Errorf("unknown probe deployment")
		}
		if _, ok = p.Doc.Services[dep.Service].Implementations[dep.Implementation].Endpoints[probe.Interface]; !ok {
			return fmt.Errorf("probe interface must be declared by the selected implementation")
		}
		contract := p.Doc.Services[dep.Service].Provides[probe.Interface]
		if len(probe.Contains) > 4096 || len(probe.Request) > 32768 {
			return fmt.Errorf("probe assertion or request too large")
		}
		switch contract.Protocol {
		case "http":
			u, err := url.ParseRequestURI(probe.Path)
			if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(probe.Path, "/") || strings.HasPrefix(probe.Path, "//") {
				return fmt.Errorf("HTTP probe needs a local absolute path")
			}
			code, err := strconv.Atoi(probe.ExpectCode)
			if err != nil || code < 100 || code > 599 {
				return fmt.Errorf("HTTP expect_code must be 100..599")
			}
			if probe.Method != "" || len(probe.Request) > 0 {
				return fmt.Errorf("HTTP probes use GET and no body")
			}
		case "grpc":
			if probe.Path != "" {
				return fmt.Errorf("gRPC probe cannot have HTTP path")
			}
			if !strings.HasPrefix(probe.Method, contract.Service+"/") {
				return fmt.Errorf("gRPC method must belong to %s", contract.Service)
			}
			svc := p.contracts[dep.Service+"."+probe.Interface]
			if svc == nil {
				return fmt.Errorf("missing compiled contract")
			}

			method := svc.Methods().ByName(protoreflect.Name(strings.TrimPrefix(probe.Method, contract.Service+"/")))
			if method == nil || method.IsStreamingClient() || method.IsStreamingServer() {
				return fmt.Errorf("unknown or streaming method")
			}
			if len(probe.Request) == 0 {
				return fmt.Errorf("gRPC request object required")
			}
			if err := protojson.Unmarshal(probe.Request, dynamicpb.NewMessage(method.Input())); err != nil {
				return err
			}
			if !validGRPCCode(probe.ExpectCode) {
				return fmt.Errorf("invalid expected gRPC code %q; use OK, Canceled, Unknown, InvalidArgument, DeadlineExceeded, NotFound, AlreadyExists, PermissionDenied, ResourceExhausted, FailedPrecondition, Aborted, OutOfRange, Unimplemented, Internal, Unavailable, DataLoss or Unauthenticated. This is a planned assertion, not a claim of execution", probe.ExpectCode)
			}
		default:
			return fmt.Errorf("probe protocol %s unsupported; declare an HTTP/gRPC entrypoint", contract.Protocol)
		}
	}
	return nil
}
func (p *Project) Export(ctx context.Context) (string, error) {
	path, err := loader.Export(p.Doc, p.Dir)
	if err != nil {
		return "", err
	}
	doc, err := loader.LoadContext(ctx, path)
	if err != nil {
		return "", err
	}
	spec, err := scenarioapp.Resolve(doc.Scenario, doc.Services, doc.Files)
	if err != nil {
		return "", err
	}
	graph, err := scenario.DependencyGraph(doc.Scenario, doc.Services)
	if err != nil {
		return "", err
	}
	if err = p.Save("graph.json", graph); err != nil {
		return "", err
	}
	configs := map[string]map[string]string{}
	for name, w := range spec.Workloads {
		configs[name] = w.Env
	}
	if err = p.Save("config-snapshot.json", configs); err != nil {
		return "", err
	}
	if err = p.Save("execution-spec.json", spec); err != nil {
		return "", err
	}
	return path, nil
}
func (p *Project) Save(name string, value any) error {
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid artifact name")
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(p.Dir, ".artifact-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(p.Dir, name))
}
func schema(m protoreflect.MessageDescriptor, depth int) any {
	if depth > 3 {
		return map[string]string{"message": string(m.FullName())}
	}
	out := map[string]any{}
	for i := 0; i < m.Fields().Len(); i++ {
		f := m.Fields().Get(i)
		v := map[string]any{"type": f.Kind().String(), "repeated": f.IsList(), "map": f.IsMap()}
		if f.Message() != nil {
			v["fields"] = schema(f.Message(), depth+1)
		}
		if f.Enum() != nil {
			values := []string{}
			for j := 0; j < f.Enum().Values().Len(); j++ {
				values = append(values, string(f.Enum().Values().Get(j).Name()))
			}
			v["values"] = values
		}
		out[f.JSONName()] = v
	}
	return out
}
func secretKey(key string) bool {
	n := strings.ToUpper(key)
	return strings.Contains(n, "PASSWORD") || strings.Contains(n, "SECRET") || strings.Contains(n, "TOKEN") || strings.HasSuffix(n, "API_KEY") || n == "PRIVATE_KEY"
}

var grpcCodes = []string{"OK", "Canceled", "Unknown", "InvalidArgument", "DeadlineExceeded", "NotFound", "AlreadyExists", "PermissionDenied", "ResourceExhausted", "FailedPrecondition", "Aborted", "OutOfRange", "Unimplemented", "Internal", "Unavailable", "DataLoss", "Unauthenticated"}

func canonicalGRPCCode(code string) string {
	value := strings.ReplaceAll(code, "_", "")
	for _, candidate := range grpcCodes {
		if strings.EqualFold(value, candidate) {
			return candidate
		}
	}
	return code
}
func validGRPCCode(code string) bool {
	value := canonicalGRPCCode(code)
	for _, candidate := range grpcCodes {
		if value == candidate {
			return true
		}
	}
	return false
}
