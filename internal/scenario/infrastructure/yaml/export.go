package yaml

import (
	"encoding/json"
	"fmt"
	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"os"
	"path/filepath"

	mock "github.com/daidaidahaidai/local-mock/internal/mocking/domain"
	"gopkg.in/yaml.v3"
)

func ComponentFragment(name string, component catalog.Component) ([]byte, error) {
	return yaml.Marshal(map[string]any{"components": map[string]componentDTO{name: componentToDTO(component)}})
}

// Export emits ordinary editable DSL. Source and IDL references remain absolute
// local repository paths; execution later snapshots them through the normal runtime.
func Export(doc Document, dir string) (string, error) {
	raw := scenarioDTO{APIVersion: Version, Kind: "Scenario", Metadata: metadata{Name: doc.Scenario.Name}, Deployments: map[string]deploymentDTO{}, Bindings: doc.Scenario.Bindings}
	raw.Components = map[string]componentDTO{}
	for name, c := range doc.Scenario.Components {
		raw.Components[name] = componentToDTO(c)
	}
	selected := map[string]map[string]bool{}
	for name, d := range doc.Scenario.Deployments {
		value := deploymentDTO{Service: d.Service, Implementation: d.Implementation, Publish: d.Publish}
		value.Config.Env = d.Env
		raw.Deployments[name] = value
		if selected[d.Service] == nil {
			selected[d.Service] = map[string]bool{}
		}
		selected[d.Service][d.Implementation] = true
	}
	for _, name := range sortedKeys(selected) {
		svc := doc.Services[name]
		out := serviceDTO{APIVersion: Version, Kind: "Service", Metadata: metadata{Name: name}, Provides: map[string]interfaceDTO{}, Implementations: map[string]implementationDTO{}}
		for k, v := range svc.Provides {
			out.Provides[k] = interfaceDTO{Protocol: v.Protocol, Contract: v.Contract, Service: v.Service}
		}
		for _, implName := range sortedKeys(selected[name]) {
			impl := svc.Implementations[implName]
			value := implementationDTO{Type: impl.Type, Role: impl.Role, Image: impl.Image, Command: impl.Command, Env: impl.Env, Endpoints: map[string]endpointDTO{}, Dependencies: map[string]dependencyDTO{}}
			value.Readiness.Command = impl.Readiness.Command
			if impl.Build != nil {
				value.Build = &buildDTO{Context: impl.Build.Context, Dockerfile: impl.Build.Dockerfile, Args: impl.Build.Args}
			}
			for k, v := range impl.Endpoints {
				value.Endpoints[k] = endpointDTO{Port: v.Port}
			}
			for k, v := range impl.Dependencies {
				dep := dependencyDTO{Service: v.Service, Interfaces: v.Interfaces}
				dep.Inject.Env = v.Env
				value.Dependencies[k] = dep
			}
			if len(impl.MockBundle) > 0 {
				var bundle mock.Bundle
				if err := json.Unmarshal(impl.MockBundle, &bundle); err != nil {
					return "", err
				}
				var contract string
				for ep := range impl.Endpoints {
					contract = svc.Provides[ep].Contract
				}
				rules := []any{}
				for _, r := range bundle.Rules {
					rule := map[string]any{"method": r.Method, "delayMs": r.DelayMS}
					if len(r.Response) > 0 {
						var response yaml.Node
						if err := yaml.Unmarshal(r.Response, &response); err != nil {
							return "", err
						}
						rule["response"] = response.Content[0]
					} else {
						rule["error"] = map[string]string{"code": r.ErrorCode, "message": r.ErrorMessage}
					}
					rules = append(rules, rule)
				}
				file := name + "--" + implName + ".mock.yaml"
				if err := writeYAML(filepath.Join(dir, file), map[string]any{"apiVersion": Version, "kind": "Mock", "metadata": map[string]string{"name": name}, "protocol": "grpc", "contract": contract, "service": bundle.Service, "rules": rules}); err != nil {
					return "", err
				}
				value.Mock = file
				value.Command = nil
				value.Readiness.Command = nil
			}
			out.Implementations[implName] = value
		}
		file := name + ".service.yaml"
		if err := writeYAML(filepath.Join(dir, file), out); err != nil {
			return "", err
		}
		raw.Catalog = append(raw.Catalog, file)
	}
	path := filepath.Join(dir, "scenario.yaml")
	return path, writeYAML(path, raw)
}
func writeYAML(path string, value any) error {
	b, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create artifact: %w", err)
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
