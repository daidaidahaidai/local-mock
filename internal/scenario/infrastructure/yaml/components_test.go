package yaml

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
