package yaml

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredMockMustImplementTheDeclaredGRPCService(t *testing.T) {
	dir := t.TempDir()
	contract := `syntax="proto3"; package fixture; service Payment { rpc Charge(Empty) returns (Empty); } service Cart { rpc Charge(Empty) returns (Empty); } message Empty {}`
	os.WriteFile(filepath.Join(dir, "demo.proto"), []byte(contract), 0600)
	os.WriteFile(filepath.Join(dir, "mock.yaml"), []byte("apiVersion: localmock/v1alpha1\nkind: Mock\nmetadata: {name: wrong-service}\nprotocol: grpc\ncontract: demo.proto\nservice: fixture.Cart\nrules:\n  - method: Charge\n    response: {}\n"), 0600)
	service := `apiVersion: localmock/v1alpha1
kind: Service
metadata: {name: payment}
provides:
  grpc: {protocol: grpc, contract: demo.proto, service: fixture.Payment}
implementations:
  mock:
    type: mock
    role: mock
    mock: mock.yaml
    endpoints: {grpc: {port: 50051}}
`
	path := filepath.Join(dir, "service.yaml")
	os.WriteFile(path, []byte(service), 0600)
	if _, err := loadServiceContext(context.Background(), path, map[string]string{}); err == nil {
		t.Fatal("accepted different protobuf service from the same file")
	}
}

func TestMockContractIncludesImportedMessages(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "alternate"), 0700); err != nil {
		t.Fatal(err)
	}
	contract := `syntax="proto3"; package fixture; import "types.proto"; service Payment { rpc Charge(Message) returns (Message); }`
	message := `syntax="proto3"; package fixture; message Message { string value = 1; }`
	write("demo.proto", contract)
	write("types.proto", message)
	write("alternate/demo.proto", contract)
	write("alternate/types.proto", message)
	write("mock.yaml", "apiVersion: localmock/v1alpha1\nkind: Mock\nmetadata: {name: payment}\nprotocol: grpc\ncontract: alternate/demo.proto\nservice: fixture.Payment\nrules:\n  - method: Charge\n    response: {}\n")
	write("service.yaml", `apiVersion: localmock/v1alpha1
kind: Service
metadata: {name: payment}
provides:
  grpc: {protocol: grpc, contract: demo.proto, service: fixture.Payment}
implementations:
  mock:
    type: mock
    role: mock
    mock: mock.yaml
    endpoints: {grpc: {port: 50051}}
`)
	path := filepath.Join(dir, "service.yaml")
	if _, err := loadService(path, map[string]string{}); err != nil {
		t.Fatalf("identical imported definitions rejected: %v", err)
	}
	write("alternate/types.proto", strings.Replace(message, "string value", "int32 value", 1))
	if _, err := loadService(path, map[string]string{}); err == nil || !strings.Contains(err.Error(), "contract must match") {
		t.Fatalf("expected imported contract mismatch, got %v", err)
	}
}

func TestRejectsUnknownDuplicateAndMultipleDocuments(t *testing.T) {
	for name, input := range map[string]string{
		"unknown":   "apiVersion: localmock/v1alpha1\nkind: Scenario\nmetadtaa: {}\n",
		"duplicate": "apiVersion: localmock/v1alpha1\nkind: Scenario\nkind: Service\n",
		"multiple":  "apiVersion: localmock/v1alpha1\nkind: Scenario\n---\nkind: Scenario\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scenario.yaml")
			os.WriteFile(path, []byte(input), 0600)
			if _, err := Load(path); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}
func TestLoadSmokeScenario(t *testing.T) {
	doc, err := Load("../../../../examples/http-smoke/scenario.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Services) != 1 || len(doc.Scenario.Deployments) != 2 || len(doc.Files) < 4 {
		t.Fatalf("unexpected document: %+v", doc)
	}
	for path, hash := range doc.Files {
		if !filepath.IsAbs(path) || len(hash) != 64 {
			t.Fatalf("bad input fingerprint %s %s", path, hash)
		}
	}
}

func TestMockSelectionDoesNotRequireUnselectedSource(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "contract"), []byte("fixture contract"), 0600)
	service := `apiVersion: localmock/v1alpha1
kind: Service
metadata: {name: payment}
provides:
  api: {protocol: http, contract: contract}
implementations:
  real:
    type: source
    role: real
    build: {context: source-not-downloaded, dockerfile: Dockerfile}
    endpoints: {api: {port: 8080}}
    readiness: {command: [/health]}
  mock:
    type: image
    role: mock
    image: example/mock:fixture
    endpoints: {api: {port: 8080}}
    readiness: {command: [/health]}
`
	os.WriteFile(filepath.Join(dir, "service.yaml"), []byte(service), 0600)
	scenario := `apiVersion: localmock/v1alpha1
kind: Scenario
metadata: {name: only-mock}
catalog: [service.yaml]
deployments:
  payment: {service: payment, implementation: mock}
`
	path := filepath.Join(dir, "scenario.yaml")
	os.WriteFile(path, []byte(scenario), 0600)
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(strings.Replace(scenario, "implementation: mock", "implementation: real", 1)), 0600)
	if _, err := Load(path); err == nil {
		t.Fatal("selected missing source was accepted")
	}
}
func TestDockerfileCannotEscapeContext(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "contract"), []byte("contract"), 0600)
	os.Mkdir(filepath.Join(dir, "app"), 0700)
	os.WriteFile(filepath.Join(dir, "secret"), []byte("secret"), 0600)
	service := `apiVersion: localmock/v1alpha1
kind: Service
metadata: {name: example}
provides:
  api: {protocol: http, contract: contract}
implementations:
  real:
    type: source
    role: real
    build: {context: app, dockerfile: ../secret}
    readiness: {command: [true]}
`
	path := filepath.Join(dir, "service.yaml")
	os.WriteFile(path, []byte(service), 0600)
	_, err := loadService(path, map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("%v", err)
	}
}
