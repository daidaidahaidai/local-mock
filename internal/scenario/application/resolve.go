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
	spec := execution.Spec{Name: s.Name, Inputs: inputs, Workloads: map[string]execution.Workload{}}
	for name, deployment := range s.Deployments {
		impl := services[deployment.Service].Implementations[deployment.Implementation]
		env, err := domain.ResolveEnv(name, s, services)
		if err != nil {
			return spec, err
		}
		w := execution.Workload{Service: deployment.Service, Role: impl.Role, Image: impl.Image, Command: impl.Command, Env: env, Readiness: impl.Readiness.Command}
		if impl.Build != nil {
			w.Build = &execution.BuildSpec{Context: impl.Build.Context, Dockerfile: impl.Build.Dockerfile}
		}
		for endpoint, hostPort := range deployment.Publish {
			w.Publish = append(w.Publish, execution.PublishedPort{Target: impl.Endpoints[endpoint].Port, Published: hostPort})
		}
		sort.Slice(w.Publish, func(i, j int) bool { return w.Publish[i].Target < w.Publish[j].Target })
		if len(w.Readiness) == 0 {
			return spec, fmt.Errorf("%s: readiness is required", name)
		}
		spec.Workloads[name] = w
	}
	return spec, nil
}
