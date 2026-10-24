package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"github.com/daidaidahaidai/local-mock/internal/catalog/infrastructure/components"
	probeimpl "github.com/daidaidahaidai/local-mock/internal/execution/infrastructure/probe"
	"github.com/daidaidahaidai/local-mock/internal/orchestration/domain"
	scenario "github.com/daidaidahaidai/local-mock/internal/scenario/domain"
	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
)

func smokeProject(t *testing.T) *Project {
	t.Helper()
	repo, err := filepath.Abs("../../../../examples/http-smoke")
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(context.Background(), repo, filepath.Join(repo, "scenario.yaml"), filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestProjectRejectsInvalidHandoffsAndExportsExecutableDSL(t *testing.T) {
	p := smokeProject(t)
	top := topologyOf(p.Seed)
	top.Reason = "single real caller with whole mock dependency"
	if err := p.CheckTopology(top); err != nil {
		t.Fatal(err)
	}
	p.ApplyTopology(top)
	top.Bindings = map[string]map[string]string{}
	if err := p.CheckTopology(top); err == nil {
		t.Fatal("accepted unbound dependency")
	}
	for _, env := range []map[string]map[string]string{{"caller": {"BACKEND_URL": "http://outside"}}, {"missing": {"X": "Y"}}, {"caller": {"DEEPSEEK_API_KEY": "never-use"}}} {
		if err := p.CheckConfig(domain.Config{Env: env, Reason: "invalid"}); err == nil {
			t.Fatal("accepted invalid env")
		}
	}
	config := domain.Config{Env: map[string]map[string]string{"dependency": {"MOCK_RESPONSE": "agent-created"}}, Reason: "task fixture"}
	if err := p.CheckConfig(config); err != nil {
		t.Fatal(err)
	}
	p.ApplyConfig(config)
	if err := p.CheckMocks(domain.Mocks{Mocks: []domain.Mock{{Service: "echo", Implementation: "mock"}}, Reason: "wrong kind"}); err == nil {
		t.Fatal("rewrote code mock using grpc rules")
	}
	runtime := domain.Runtime{Reason: "real request", Probes: []domain.Probe{{Name: "entry", Deployment: "caller", Interface: "api", Path: "/", ExpectCode: "200", Contains: "agent-created"}}}
	if err := p.CheckRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"https://elsewhere/", "//elsewhere/"} {
		r := runtime
		r.Probes = append([]domain.Probe(nil), runtime.Probes...)
		r.Probes[0].Path = path
		if err := p.CheckRuntime(r); err == nil {
			t.Fatal("accepted external probe")
		}
	}
	file, err := p.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loader.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if problems := scenario.Validate(doc.Scenario, doc.Services); len(problems) > 0 {
		t.Fatal(problems)
	}
	if doc.Scenario.Deployments["dependency"].Env["MOCK_RESPONSE"] != "agent-created" {
		t.Fatal("lost config")
	}
	if _, err = New(context.Background(), p.Repo.Root, filepath.Join(p.Repo.Root, "scenario.yaml"), p.Dir); err == nil {
		t.Fatal("overwrote existing workflow")
	}
}
func TestConfiguredMockRulesAreValidatedAndExported(t *testing.T) {
	root := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("service.proto", `syntax="proto3"; package fixture; message Input {} message Output { string value = 1; } service Echo { rpc Call(Input) returns (Output); }`)
	write("mock.yaml", `apiVersion: localmock/v1alpha1
kind: Mock
metadata: {name: echo}
protocol: grpc
contract: service.proto
service: fixture.Echo
rules:
  - method: Call
    response: {value: original}
`)
	write("service.yaml", `apiVersion: localmock/v1alpha1
kind: Service
metadata: {name: echo}
provides:
  grpc: {protocol: grpc, contract: service.proto, service: fixture.Echo}
implementations:
  mock:
    type: mock
    role: mock
    mock: mock.yaml
    endpoints: {grpc: {port: 50051}}
`)
	write("scenario.yaml", `apiVersion: localmock/v1alpha1
kind: Scenario
metadata: {name: grpc-example}
catalog: [service.yaml]
deployments:
  echo: {service: echo, implementation: mock}
`)
	p, err := New(context.Background(), root, filepath.Join(root, "scenario.yaml"), filepath.Join(root, "out"))
	if err != nil {
		t.Fatal(err)
	}
	contextJSON, _ := json.Marshal(p.Context("mock"))
	if !strings.Contains(string(contextJSON), `"value":"original"`) {
		t.Fatalf("model received base64 instead of mock response: %s", contextJSON)
	}
	mocks := domain.Mocks{Reason: "failure test", Mocks: []domain.Mock{{Service: "echo", Implementation: "mock", Rules: []domain.Rule{{Method: "Call", ErrorCode: "UNAVAILABLE", ErrorMessage: "outage"}}}}}
	if err = p.CheckMocks(mocks); err != nil {
		t.Fatal(err)
	}
	p.ApplyMocks(mocks)
	r := domain.Runtime{Reason: "assert outage", Probes: []domain.Probe{{Name: "call", Deployment: "echo", Interface: "grpc", Method: "fixture.Echo/Call", Request: json.RawMessage(`{}`), ExpectCode: "UNAVAILABLE", Contains: "outage"}}}
	if err = p.CheckRuntime(r); err != nil {
		t.Fatal(err)
	}
	r.Probes[0].Request = json.RawMessage(`{"invented":true}`)
	if err = p.CheckRuntime(r); err == nil {
		t.Fatal("accepted unknown protobuf field")
	}
	file, err := p.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loader.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc.Services["echo"].Implementations["mock"].MockBundle), "outage") {
		t.Fatal("mock edit lost in DSL export")
	}
	mocks.Mocks[0].Rules[0].ErrorCode = "NOT_A_CODE"
	if err = p.CheckMocks(mocks); err == nil {
		t.Fatal("accepted invalid status")
	}
}
func TestHTTPProbeDoesNotFollowRedirects(t *testing.T) {
	reached := false
	external := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer external.Close()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("traceparent") == "" {
			t.Error("no trace context")
		}
		http.Redirect(w, r, external.URL, 302)
	}))
	defer local.Close()
	code := probeimpl.HTTP(context.Background(), strings.TrimPrefix(local.URL, "http://"), "/").Code
	if code != "302" || reached {
		t.Fatalf("code=%s followed=%v", code, reached)
	}
}

func TestUserComponentSelectionSurvivesAgentHandoffs(t *testing.T) {
	p := smokeProject(t)
	p.Seed.Components = components.Templates()
	cache := p.Seed.Components["redis"]
	cache.Initialize = []catalog.Initialization{{Name: "user-seed", Command: []string{"redis-cli", "-e", "SET", "fixture", "user-owned"}}}
	p.Seed.Components["redis"] = cache
	p.Doc.Scenario.Components = p.Seed.Components
	d := p.Seed.Deployments["caller"]
	d.Env = map[string]string{"REGISTRY": "${component.nacos.http.address}"}
	p.Seed.Deployments["caller"] = d
	top := topologyOf(p.Seed)
	top.Reason = "keep services"
	if err := p.CheckTopology(top); err != nil {
		t.Fatal(err)
	}
	p.ApplyTopology(top)
	if err := p.CheckConfig(domain.Config{Reason: "override", Env: map[string]map[string]string{"caller": {"REGISTRY": "elsewhere:8848"}}}); err == nil {
		t.Fatal("agent replaced user component binding")
	}
	config := domain.Config{Reason: "no additional configuration", Env: map[string]map[string]string{}}
	if err := p.CheckConfig(config); err != nil {
		t.Fatal(err)
	}
	p.ApplyConfig(config)
	if len(p.Doc.Scenario.Components) != 3 || p.Doc.Scenario.Deployments["caller"].Env["REGISTRY"] != "${component.nacos.http.address}" {
		t.Fatal("lost user selections")
	}
	file, err := p.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loader.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Scenario.Components) != 3 {
		t.Fatal("components missing from exported DSL")
	}
	steps := doc.Scenario.Components["redis"].Initialize
	if len(steps) != 1 || steps[0].Name != "user-seed" || steps[0].Command[4] != "user-owned" {
		t.Fatal("user initialization lost during agent handoffs")
	}
}
