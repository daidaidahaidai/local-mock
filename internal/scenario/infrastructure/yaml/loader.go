package yaml

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"github.com/daidaidahaidai/local-mock/internal/mocking/infrastructure/grpcmock"
	scenario "github.com/daidaidahaidai/local-mock/internal/scenario/domain"
	"gopkg.in/yaml.v3"
)

const Version = "localmock/v1alpha1"
const maxFileSize = 4 << 20

type metadata struct {
	Name string `yaml:"name"`
}
type interfaceDTO struct {
	Protocol string `yaml:"protocol"`
	Contract string `yaml:"contract"`
	Service  string `yaml:"service"`
}
type endpointDTO struct {
	Port int `yaml:"port"`
}
type buildDTO struct {
	Context    string            `yaml:"context"`
	Dockerfile string            `yaml:"dockerfile"`
	Args       map[string]string `yaml:"args"`
}
type dependencyDTO struct {
	Service    string   `yaml:"service"`
	Interfaces []string `yaml:"interfaces"`
	Inject     struct {
		Env map[string]string `yaml:"env"`
	} `yaml:"inject"`
}
type implementationDTO struct {
	Type         string                   `yaml:"type"`
	Role         string                   `yaml:"role"`
	Image        string                   `yaml:"image"`
	Mock         string                   `yaml:"mock"`
	Build        *buildDTO                `yaml:"build"`
	Command      []string                 `yaml:"command"`
	Env          map[string]string        `yaml:"env"`
	Endpoints    map[string]endpointDTO   `yaml:"endpoints"`
	Dependencies map[string]dependencyDTO `yaml:"dependencies"`
	Readiness    struct {
		Command []string `yaml:"command"`
	} `yaml:"readiness"`
}
type serviceDTO struct {
	APIVersion      string                       `yaml:"apiVersion"`
	Kind            string                       `yaml:"kind"`
	Metadata        metadata                     `yaml:"metadata"`
	Provides        map[string]interfaceDTO      `yaml:"provides"`
	Implementations map[string]implementationDTO `yaml:"implementations"`
}
type deploymentDTO struct {
	Service        string `yaml:"service"`
	Implementation string `yaml:"implementation"`
	Config         struct {
		Env map[string]string `yaml:"env"`
	} `yaml:"config"`
	Publish map[string]int `yaml:"publish"`
}
type scenarioDTO struct {
	APIVersion  string                       `yaml:"apiVersion"`
	Kind        string                       `yaml:"kind"`
	Metadata    metadata                     `yaml:"metadata"`
	Catalog     []string                     `yaml:"catalog"`
	Deployments map[string]deploymentDTO     `yaml:"deployments"`
	Bindings    map[string]map[string]string `yaml:"bindings"`
	Components  map[string]componentDTO      `yaml:"components,omitempty"`
}
type componentDTO struct {
	Roles     []string          `yaml:"roles"`
	Image     string            `yaml:"image"`
	Command   []string          `yaml:"command,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`
	Endpoints map[string]int    `yaml:"endpoints"`
	IPOffset  int               `yaml:"ipOffset"`
	MemoryMB  int               `yaml:"memoryMB,omitempty"`
	Readiness struct {
		Command []string `yaml:"command"`
	} `yaml:"readiness"`
}

func componentFromDTO(c componentDTO) catalog.Component {
	return catalog.Component{Roles: c.Roles, Image: c.Image, Command: c.Command, Env: c.Env, Endpoints: c.Endpoints, IPOffset: c.IPOffset, MemoryMB: c.MemoryMB, Readiness: c.Readiness.Command}
}
func componentToDTO(c catalog.Component) componentDTO {
	v := componentDTO{Roles: c.Roles, Image: c.Image, Command: c.Command, Env: c.Env, Endpoints: c.Endpoints, IPOffset: c.IPOffset, MemoryMB: c.MemoryMB}
	v.Readiness.Command = c.Readiness
	return v
}

type Document struct {
	Scenario scenario.Scenario
	Services map[string]catalog.Service
	Files    map[string]string // absolute path -> SHA-256 of loaded input
}

func Load(path string) (Document, error) {
	return LoadContext(context.Background(), path)
}
func LoadContext(ctx context.Context, path string) (Document, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Document{}, err
	}
	doc := Document{Services: map[string]catalog.Service{}, Files: map[string]string{}}
	var raw scenarioDTO
	if err = decode(absolute, &raw, doc.Files); err != nil {
		return doc, err
	}
	if raw.APIVersion != Version || raw.Kind != "Scenario" {
		return doc, fmt.Errorf("expected %s Scenario", Version)
	}
	if len(raw.Catalog) == 0 && len(raw.Components) == 0 {
		return doc, fmt.Errorf("catalog must not be empty unless components are explicitly selected")
	}
	for _, reference := range raw.Catalog {
		file := resolve(filepath.Dir(absolute), reference)
		svc, err := loadServiceContext(ctx, file, doc.Files)
		if err != nil {
			return doc, fmt.Errorf("catalog %s: %w", reference, err)
		}
		if _, ok := doc.Services[svc.Name]; ok {
			return doc, fmt.Errorf("duplicate service %s", svc.Name)
		}
		doc.Services[svc.Name] = svc
	}
	doc.Scenario = scenario.Scenario{Name: raw.Metadata.Name, Deployments: map[string]scenario.Deployment{}, Bindings: raw.Bindings}
	doc.Scenario.Components = map[string]catalog.Component{}
	for name, c := range raw.Components {
		doc.Scenario.Components[name] = componentFromDTO(c)
	}
	for name, d := range raw.Deployments {
		doc.Scenario.Deployments[name] = scenario.Deployment{Service: d.Service, Implementation: d.Implementation, Env: d.Config.Env, Publish: d.Publish}
	}
	// A mock selection must not require the unselected real implementation's source.
	for _, name := range sortedKeys(doc.Scenario.Deployments) {
		deployment := doc.Scenario.Deployments[name]
		impl := doc.Services[deployment.Service].Implementations[deployment.Implementation]
		if impl.Build == nil {
			continue
		}
		info, err := os.Stat(impl.Build.Context)
		if err != nil {
			return doc, fmt.Errorf("deployment %s: %w", name, err)
		}
		if !info.IsDir() {
			return doc, fmt.Errorf("deployment %s build context is not a directory", name)
		}
		file := filepath.Join(impl.Build.Context, impl.Build.Dockerfile)
		content, err := readFile(file)
		if err != nil {
			return doc, fmt.Errorf("deployment %s: %w", name, err)
		}
		doc.Files[file] = fmt.Sprintf("%x", sha256.Sum256(content))
	}
	return doc, nil
}

func loadService(path string, files map[string]string) (catalog.Service, error) {
	return loadServiceContext(context.Background(), path, files)
}
func loadServiceContext(ctx context.Context, path string, files map[string]string) (catalog.Service, error) {
	var raw serviceDTO
	if err := decode(path, &raw, files); err != nil {
		return catalog.Service{}, err
	}
	if raw.APIVersion != Version || raw.Kind != "Service" {
		return catalog.Service{}, fmt.Errorf("expected %s Service", Version)
	}
	if !scenario.ValidName(raw.Metadata.Name) {
		return catalog.Service{}, fmt.Errorf("invalid service name")
	}
	svc := catalog.Service{Name: raw.Metadata.Name, Provides: map[string]catalog.Interface{}, Implementations: map[string]catalog.Implementation{}}
	for _, name := range sortedKeys(raw.Provides) {
		i := raw.Provides[name]
		if !scenario.ValidName(name) {
			return svc, fmt.Errorf("invalid interface name %s", name)
		}
		switch i.Protocol {
		case "http", "https", "grpc", "thrift", "custom":
		default:
			return svc, fmt.Errorf("unsupported protocol declaration %s", i.Protocol)
		}
		if i.Contract == "" {
			return svc, fmt.Errorf("interface %s requires a contract file", name)
		}
		contract := resolve(filepath.Dir(path), i.Contract)
		content, err := readFile(contract)
		if err != nil {
			return svc, err
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(content))
		files[contract] = hash
		var compiled []byte
		if i.Protocol == "grpc" {
			if i.Service == "" {
				return svc, fmt.Errorf("gRPC interface %s requires a fully qualified service", name)
			}
			descriptors, inputs, err := grpcmock.Compile(ctx, contract)
			if err != nil {
				return svc, err
			}
			if _, err = grpcmock.Service(descriptors, i.Service); err != nil {
				return svc, err
			}
			hash = fmt.Sprintf("%x", sha256.Sum256(descriptors))
			compiled = descriptors
			for path, hash := range inputs {
				files[path] = hash
			}
		} else if i.Service != "" {
			return svc, fmt.Errorf("service qualifier is only supported for gRPC")
		}
		svc.Provides[name] = catalog.Interface{Protocol: i.Protocol, Contract: contract, ContractHash: hash, Service: i.Service, Descriptors: compiled}
	}
	if len(raw.Implementations) == 0 {
		return svc, fmt.Errorf("at least one implementation is required")
	}
	for _, name := range sortedKeys(raw.Implementations) {
		i := raw.Implementations[name]
		if !scenario.ValidName(name) {
			return svc, fmt.Errorf("invalid implementation name %s", name)
		}
		if i.Role != "real" && i.Role != "mock" {
			return svc, fmt.Errorf("implementation %s role must be real or mock", name)
		}
		impl := catalog.Implementation{Type: i.Type, Role: i.Role, Image: i.Image, Command: i.Command, Env: i.Env, Endpoints: map[string]catalog.Endpoint{}, Dependencies: map[string]catalog.Dependency{}, Readiness: catalog.Readiness{Command: i.Readiness.Command}}
		switch i.Type {
		case "image":
			if i.Image == "" || i.Build != nil || i.Mock != "" {
				return svc, fmt.Errorf("image implementation %s requires image and forbids build", name)
			}
		case "source":
			if i.Build == nil || i.Image != "" || i.Mock != "" || i.Build.Context == "" || i.Build.Dockerfile == "" {
				return svc, fmt.Errorf("source implementation %s requires build context/dockerfile and forbids image", name)
			}
			context := resolve(filepath.Dir(path), i.Build.Context)
			if filepath.IsAbs(i.Build.Dockerfile) {
				return svc, fmt.Errorf("Dockerfile must be relative to build context")
			}
			file := filepath.Join(context, i.Build.Dockerfile)
			rel, err := filepath.Rel(context, file)
			if err != nil || rel == ".." || len(rel) > 3 && rel[:3] == "../" {
				return svc, fmt.Errorf("Dockerfile escapes build context")
			}
			for key := range i.Build.Args {
				if !validEnv(key) {
					return svc, fmt.Errorf("invalid build argument %s", key)
				}
			}
			impl.Build = &catalog.Build{Context: context, Dockerfile: i.Build.Dockerfile, Args: i.Build.Args}
		case "mock":
			if i.Role != "mock" || i.Mock == "" || i.Image != "" || i.Build != nil || len(i.Command) > 0 || len(i.Readiness.Command) > 0 || len(i.Dependencies) > 0 || len(i.Endpoints) != 1 {
				return svc, fmt.Errorf("configured mock %s requires role mock, one endpoint and mock file; image/build/command/readiness/dependencies are generated", name)
			}
			bundle, inputs, _, err := grpcmock.Load(ctx, resolve(filepath.Dir(path), i.Mock))
			if err != nil {
				return svc, fmt.Errorf("mock %s: %w", name, err)
			}
			for endpoint, value := range i.Endpoints {
				provided, ok := svc.Provides[endpoint]
				if !ok || provided.Protocol != "grpc" || provided.ContractHash != fmt.Sprintf("%x", sha256.Sum256(bundle.Descriptors)) || provided.Service != bundle.Service {
					return svc, fmt.Errorf("mock contract must match its provided gRPC interface")
				}
				impl.Readiness.Command = []string{"/localmock", "mock", "health", "--address", "127.0.0.1:" + strconv.Itoa(value.Port)}
				impl.Command = []string{"mock", "serve", "--bundle", "/bundle.json", "--listen", ":" + strconv.Itoa(value.Port)}
			}
			for path, hash := range inputs {
				files[path] = hash
			}
			impl.MockBundle, err = json.Marshal(bundle)
			if err != nil {
				return svc, err
			}
		default:
			return svc, fmt.Errorf("unsupported implementation type %s", i.Type)
		}
		if len(impl.Readiness.Command) == 0 {
			return svc, fmt.Errorf("implementation %s requires a readiness command", name)
		}
		for key := range i.Env {
			if !validEnv(key) {
				return svc, fmt.Errorf("invalid environment key %s", key)
			}
		}
		for epName, ep := range i.Endpoints {
			if _, ok := svc.Provides[epName]; !ok {
				return svc, fmt.Errorf("endpoint %s is not provided by service", epName)
			}
			if ep.Port < 1 || ep.Port > 65535 {
				return svc, fmt.Errorf("invalid port for %s", epName)
			}
			impl.Endpoints[epName] = catalog.Endpoint{Port: ep.Port}
		}
		for depName, dep := range i.Dependencies {
			if !scenario.ValidName(depName) || !scenario.ValidName(dep.Service) {
				return svc, fmt.Errorf("invalid dependency %s", depName)
			}
			if len(dep.Interfaces) == 0 {
				return svc, fmt.Errorf("dependency %s requires interfaces", depName)
			}
			seen := map[string]bool{}
			for _, iface := range dep.Interfaces {
				if !scenario.ValidName(iface) || seen[iface] {
					return svc, fmt.Errorf("invalid or duplicate dependency interface %s", iface)
				}
				seen[iface] = true
			}
			for key := range dep.Inject.Env {
				if !validEnv(key) {
					return svc, fmt.Errorf("invalid environment key %s", key)
				}
			}
			impl.Dependencies[depName] = catalog.Dependency{Service: dep.Service, Interfaces: dep.Interfaces, Env: dep.Inject.Env}
		}
		svc.Implementations[name] = impl
	}
	return svc, nil
}

func decode(path string, target any, files map[string]string) error {
	content, err := readFile(path)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err = decoder.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%s: multiple YAML documents are not allowed", path)
		}
		return err
	}
	files[path] = fmt.Sprintf("%x", sha256.Sum256(content))
	return nil
}
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxFileSize {
		return nil, fmt.Errorf("file exceeds %d bytes: %s", maxFileSize, path)
	}
	return b, nil
}
func resolve(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

var envRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validEnv(s string) bool { return envRE.MatchString(s) }
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
