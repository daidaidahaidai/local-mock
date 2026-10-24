package yaml

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"github.com/daidaidahaidai/local-mock/internal/catalog/infrastructure/components"
	scenarioapp "github.com/daidaidahaidai/local-mock/internal/scenario/application"
)

func TestComponentExportAndExecutionIntent(t *testing.T) {
	doc, err := Load(filepath.Join("..", "..", "..", "..", "examples", "http-smoke", "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc.Scenario.Components = components.Templates()
	file, err := Export(doc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Scenario.Components, reloaded.Scenario.Components) {
		t.Fatal("component selection lost during export")
	}
	spec, err := scenarioapp.Resolve(reloaded.Scenario, reloaded.Services, reloaded.Files)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Network != "isolated" || len(spec.Workloads) != 5 || spec.Workloads["nacos"].IPOffset != 10 || spec.Workloads["mysql"].Role != "infrastructure" {
		t.Fatalf("unexpected execution intent: %+v", spec)
	}
	b, _ := os.ReadFile(file)
	b = []byte(strings.Replace(string(b), "ipOffset: 10", "ipOffset: 10\n        privileged: true", 1))
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(file); err == nil {
		t.Fatal("accepted arbitrary container privilege field")
	}
}

func TestInitializationRoundTripAndInputs(t *testing.T) {
	dir := t.TempDir()
	stdin := filepath.Join(dir, "seed.sql")
	if err := os.WriteFile(stdin, []byte("SELECT 42;"), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := Load("../../../../examples/infrastructure/scenario.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c := doc.Scenario.Components["mysql"]
	c.Initialize = []catalog.Initialization{{Name: "seed", Command: []string{"mysql"}, Stdin: stdin}}
	doc.Scenario.Components["mysql"] = c
	path, err := Export(doc, dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := scenarioapp.Resolve(loaded.Scenario, loaded.Services, loaded.Files)
	if err != nil {
		t.Fatal(err)
	}
	step := spec.Workloads["mysql"].Initialize[0]
	if step.InputHash != loaded.Files[stdin] || len(step.InputHash) != 64 || step.TimeoutSeconds != 30 {
		t.Fatalf("bad initialization snapshot: %+v", step)
	}
	if !reflect.DeepEqual(loaded.Scenario.Components, doc.Scenario.Components) {
		t.Fatal("initialization lost in exported DSL")
	}
	os.Remove(stdin)
	if _, err = Load(path); err == nil {
		t.Fatal("missing initialization file accepted")
	}
}
