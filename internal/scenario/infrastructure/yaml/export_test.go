package yaml

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	mock "github.com/daidaidahaidai/local-mock/internal/mocking/domain"
	scenario "github.com/daidaidahaidai/local-mock/internal/scenario/domain"
)

func TestExportPreservesLargeNumericMockResponses(t *testing.T) {
	bundle, _ := json.Marshal(mock.Bundle{Name: "fixture", Service: "fixture.Service", Rules: []mock.Rule{{Method: "Call", Response: []byte(`{"value":9007199254740993}`)}}})
	doc := Document{Scenario: scenario.Scenario{Name: "example", Deployments: map[string]scenario.Deployment{"svc": {Service: "svc", Implementation: "mock"}}}, Services: map[string]catalog.Service{"svc": {Name: "svc", Provides: map[string]catalog.Interface{"grpc": {Protocol: "grpc", Contract: "service.proto", Service: "fixture.Service"}}, Implementations: map[string]catalog.Implementation{"mock": {Type: "mock", Role: "mock", MockBundle: bundle, Endpoints: map[string]catalog.Endpoint{"grpc": {Port: 50051}}}}}}}
	dir := t.TempDir()
	if _, err := Export(doc, dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "svc--mock.mock.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "9007199254740993") {
		t.Fatalf("numeric response lost precision: %s", b)
	}
}
