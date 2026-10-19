package yaml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
