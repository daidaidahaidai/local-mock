package application

import (
	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"testing"
)

func TestProbesStayOnDeclaredEndpoints(t *testing.T) {
	s := domain.Spec{Workloads: map[string]domain.Workload{"app": {Endpoints: map[string]domain.Endpoint{"http": {Port: 8080, Protocol: "http"}}}}}
	p := domain.Probe{Deployment: "app", Interface: "http", Path: "/ready"}
	call, err := ResolveProbe(s, p)
	if err != nil || call.Address != "app:8080" {
		t.Fatalf("%+v %v", call, err)
	}
	for _, path := range []string{"http://outside.invalid/", "//outside.invalid/", "relative"} {
		p.Path = path
		if _, err = ResolveProbe(s, p); err == nil {
			t.Fatal("accepted external path", path)
		}
	}
	p.Path = "/"
	p.Deployment = "unselected"
	if _, err = ResolveProbe(s, p); err == nil {
		t.Fatal("accepted unselected target")
	}
}
