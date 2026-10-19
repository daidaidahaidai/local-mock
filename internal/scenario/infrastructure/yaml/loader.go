package yaml

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
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
}
type endpointDTO struct {
	Port int `yaml:"port"`
}
type buildDTO struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
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
}
type Document struct {
	Scenario scenario.Scenario
	Services map[string]catalog.Service
	Files    map[string]string // absolute path -> SHA-256 of loaded input
}

func Load(path string) (Document, error) {
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
	if len(raw.Catalog) == 0 {
		return doc, fmt.Errorf("catalog must not be empty")
	}
	for _, reference := range raw.Catalog {
		file := resolve(filepath.Dir(absolute), reference)
		svc, err := loadService(file, doc.Files)
		if err != nil {
			return doc, fmt.Errorf("catalog %s: %w", reference, err)
		}
		if _, ok := doc.Services[svc.Name]; ok {
			return doc, fmt.Errorf("duplicate service %s", svc.Name)
		}
		doc.Services[svc.Name] = svc
	}
	doc.Scenario = scenario.Scenario{Name: raw.Metadata.Name, Deployments: map[string]scenario.Deployment{}, Bindings: raw.Bindings}
	for name, d := range raw.Deployments {
		doc.Scenario.Deployments[name] = scenario.Deployment{Service: d.Service, Implementation: d.Implementation, Env: d.Config.Env, Publish: d.Publish}
	}
	return doc, nil
}

func loadService(path string, files map[string]string) (catalog.Service, error) {
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
		svc.Provides[name] = catalog.Interface{Protocol: i.Protocol, Contract: contract, ContractHash: hash}
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
			if i.Image == "" || i.Build != nil {
				return svc, fmt.Errorf("image implementation %s requires image and forbids build", name)
			}
		case "source":
			if i.Build == nil || i.Image != "" || i.Build.Context == "" || i.Build.Dockerfile == "" {
				return svc, fmt.Errorf("source implementation %s requires build context/dockerfile and forbids image", name)
			}
			context := resolve(filepath.Dir(path), i.Build.Context)
			info, err := os.Stat(context)
			if err != nil {
				return svc, err
			}
			if !info.IsDir() {
				return svc, fmt.Errorf("build context is not a directory")
			}
			if filepath.IsAbs(i.Build.Dockerfile) {
				return svc, fmt.Errorf("Dockerfile must be relative to build context")
			}
			file := filepath.Join(context, i.Build.Dockerfile)
			rel, err := filepath.Rel(context, file)
			if err != nil || rel == ".." || len(rel) > 3 && rel[:3] == "../" {
				return svc, fmt.Errorf("Dockerfile escapes build context")
			}
			content, err := readFile(file)
			if err != nil {
				return svc, err
			}
			files[file] = fmt.Sprintf("%x", sha256.Sum256(content))
			impl.Build = &catalog.Build{Context: context, Dockerfile: i.Build.Dockerfile}
		default:
			return svc, fmt.Errorf("unsupported implementation type %s", i.Type)
		}
		if len(i.Readiness.Command) == 0 {
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
