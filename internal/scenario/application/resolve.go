package application

import (
	"fmt"
	"sort"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	execution "github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"github.com/daidaidahaidai/local-mock/internal/scenario/domain"
)

func Resolve(s domain.Scenario, services map[string]catalog.Service, inputs map[string]string) (execution.Spec, error) {
	if problems := domain.Validate(s, services); len(problems) > 0 {
		return execution.Spec{}, problems[0]
	}
	spec := execution.Spec{Name: s.Name, Inputs: inputs, Workloads: map[string]execution.Workload{}, Network: "isolated"}
	names := make([]string, 0, len(s.Deployments))
	for name := range s.Deployments {
		names = append(names, name)
	}
	sort.Strings(names)
	for index, name := range names {
		deployment := s.Deployments[name]
		impl := services[deployment.Service].Implementations[deployment.Implementation]
		env, err := domain.ResolveEnv(name, s, services)
		if err != nil {
			return spec, err
		}
		w := execution.Workload{Service: deployment.Service, Role: impl.Role, Image: impl.Image, Command: impl.Command, Env: env, Readiness: impl.Readiness.Command, MockBundle: impl.MockBundle}
		w.IPOffset = 100 + index
		w.Endpoints = map[string]execution.Endpoint{}
		for key, endpoint := range impl.Endpoints {
			contract := services[deployment.Service].Provides[key]
			w.Endpoints[key] = execution.Endpoint{Port: endpoint.Port, Protocol: contract.Protocol, Descriptors: contract.Descriptors, Service: contract.Service}
		}
		if impl.Build != nil {
			w.Build = &execution.BuildSpec{Context: impl.Build.Context, Dockerfile: impl.Build.Dockerfile, Args: impl.Build.Args}
		}
		if len(w.Readiness) == 0 {
			return spec, fmt.Errorf("%s: readiness is required", name)
		}
		spec.Workloads[name] = w
	}
	for name, component := range s.Components {
		env, err := domain.ResolveComponentEnv(component.Env, s.Components)
		if err != nil {
			return spec, err
		}
		w := execution.Workload{Service: name, Role: "infrastructure", Image: component.Image, Command: component.Command, Env: env, Readiness: component.Readiness, MemoryMB: component.MemoryMB, IPOffset: component.IPOffset, Endpoints: map[string]execution.Endpoint{}}
		for key, port := range component.Endpoints {
			w.Endpoints[key] = execution.Endpoint{Port: port, Protocol: "tcp"}
		}
		spec.Workloads[name] = w
	}
	return spec, nil
}
