package domain

import (
	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"testing"
)

func componentFixture() catalog.Component {
	return catalog.Component{Roles: []string{"registry", "config"}, Image: "company/registry:1", IPOffset: 10, Endpoints: map[string]int{"rpc": 9000}, Readiness: []string{"/health"}}
}
func TestExplicitComponentsAndReferences(t *testing.T) {
	s, services := fixture()
	s.Components = map[string]catalog.Component{"registry": componentFixture()}
	d := s.Deployments["caller"]
	d.Env = map[string]string{"REGISTRY": "${component.registry.rpc.address}", "HOST": "${component.registry.rpc.host}", "PORT": "${component.registry.rpc.port}"}
	s.Deployments["caller"] = d
	if p := Validate(s, services); len(p) > 0 {
		t.Fatal(p)
	}
	env, err := ResolveEnv("caller", s, services)
	if err != nil || env["REGISTRY"] != "registry:9000" || env["HOST"] != "registry" || env["PORT"] != "9000" {
		t.Fatalf("%v %v", env, err)
	}
	delete(s.Components, "registry")
	assertProblem(t, Validate(s, services), "INVALID_COMPONENT_REFERENCE")
}
func TestComponentsRejectNetworkAndIdentityConflicts(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Scenario)
	}{
		{"same ip", func(s *Scenario) { s.Components["other"] = componentFixture() }},
		{"same name", func(s *Scenario) { s.Components["caller"] = componentFixture(); delete(s.Components, "registry") }},
		{"reserved ip", func(s *Scenario) { c := s.Components["registry"]; c.IPOffset = 2; s.Components["registry"] = c }},
		{"missing image", func(s *Scenario) { c := s.Components["registry"]; c.Image = ""; s.Components["registry"] = c }},
		{"no health", func(s *Scenario) { c := s.Components["registry"]; c.Readiness = nil; s.Components["registry"] = c }},
		{"unknown role", func(s *Scenario) {
			c := s.Components["registry"]
			c.Roles = []string{"typo"}
			s.Components["registry"] = c
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, c := fixture()
			s.Components = map[string]catalog.Component{"registry": componentFixture()}
			test.change(&s)
			assertProblem(t, Validate(s, c), "INVALID_COMPONENT")
		})
	}
}
func TestPublishingHostPortsIsRejected(t *testing.T) {
	s, c := fixture()
	d := s.Deployments["caller"]
	d.Publish = map[string]int{"api": 0}
	s.Deployments["caller"] = d
	assertProblem(t, Validate(s, c), "HOST_PORT_FORBIDDEN")
}
