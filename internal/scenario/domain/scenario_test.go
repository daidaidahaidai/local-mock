package domain

import (
	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"testing"
)

func fixture() (Scenario, map[string]catalog.Service) {
	c := map[string]catalog.Service{
		"order": {Name: "order", Implementations: map[string]catalog.Implementation{"real": {Dependencies: map[string]catalog.Dependency{"payment": {Service: "payment", Interfaces: []string{"grpc", "http"}, Env: map[string]string{"PAY": "${dependency.payment.grpc.address}"}}}}}},
		"payment": {Name: "payment", Provides: map[string]catalog.Interface{"grpc": {Protocol: "grpc"}, "http": {Protocol: "http"}}, Implementations: map[string]catalog.Implementation{
			"real": {Endpoints: map[string]catalog.Endpoint{"grpc": {Port: 50051}, "http": {Port: 8080}}, Dependencies: map[string]catalog.Dependency{"database": {Service: "database", Interfaces: []string{"sql"}}}},
			"mock": {Role: "mock", Endpoints: map[string]catalog.Endpoint{"grpc": {Port: 50051}, "http": {Port: 8080}}},
		}},
	}
	s := Scenario{Name: "example", Deployments: map[string]Deployment{"caller": {Service: "order", Implementation: "real"}, "target": {Service: "payment", Implementation: "mock"}}, Bindings: map[string]map[string]string{"caller": {"payment": "target"}}}
	return s, c
}
func TestSelectedMockDoesNotInheritRealDependencies(t *testing.T) {
	s, c := fixture()
	if p := Validate(s, c); len(p) != 0 {
		t.Fatal(p)
	}
	env, err := ResolveEnv("caller", s, c)
	if err != nil || env["PAY"] != "target:50051" {
		t.Fatalf("%v %v", env, err)
	}
}
func TestBindingsCoverWholeDependency(t *testing.T) {
	s, c := fixture()
	target := c["payment"].Implementations["mock"]
	delete(target.Endpoints, "http")
	assertProblem(t, Validate(s, c), "DEPENDENCY_INTERFACE_MISSING")
}
func TestMissingAndUnknownBindings(t *testing.T) {
	s, c := fixture()
	delete(s.Bindings["caller"], "payment")
	assertProblem(t, Validate(s, c), "DEPENDENCY_UNBOUND")
	s.Bindings["ghost"] = map[string]string{"x": "target"}
	assertProblem(t, Validate(s, c), "CALLER_NOT_FOUND")
}
func TestManagedAddressCannotBeOverridden(t *testing.T) {
	s, c := fixture()
	d := s.Deployments["caller"]
	d.Env = map[string]string{"PAY": "production:50051"}
	s.Deployments["caller"] = d
	assertProblem(t, Validate(s, c), "MANAGED_ENV_OVERRIDE")
}
func TestCannotSplitSameServiceIntoTwoDependencies(t *testing.T) {
	s, c := fixture()
	impl := c["order"].Implementations["real"]
	impl.Dependencies["refund"] = catalog.Dependency{Service: "payment", Interfaces: []string{"http"}}
	s.Bindings["caller"]["refund"] = "target"
	assertProblem(t, Validate(s, c), "DUPLICATE_SERVICE_DEPENDENCY")
}
func TestCyclicServiceBindingsAreAllowed(t *testing.T) {
	s, c := fixture()
	impl := c["payment"].Implementations["mock"]
	impl.Dependencies = map[string]catalog.Dependency{"parent": {Service: "order", Interfaces: []string{"api"}}}
	c["payment"].Implementations["mock"] = impl
	order := c["order"].Implementations["real"]
	order.Endpoints = map[string]catalog.Endpoint{"api": {Port: 8000}}
	c["order"].Implementations["real"] = order
	s.Bindings["target"] = map[string]string{"parent": "caller"}
	if p := Validate(s, c); len(p) > 0 {
		t.Fatal(p)
	}
}
func assertProblem(t *testing.T, problems []Problem, code string) {
	t.Helper()
	for _, p := range problems {
		if p.Code == code {
			return
		}
	}
	t.Fatalf("missing %s: %v", code, problems)
}
