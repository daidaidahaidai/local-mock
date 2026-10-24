package yaml

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daidaidahaidai/local-mock/internal/catalog/infrastructure/components"
	app "github.com/daidaidahaidai/local-mock/internal/scenario/application"
)

func TestComponentEditPreservesSourceAndRejectsInvalidEdits(t *testing.T) {
	dir := t.TempDir()
	// Relative catalog/build references must retain their meaning during editing.
	for _, file := range []string{"scenario.yaml", "service.yaml", "openapi.yaml"} {
		b, err := os.ReadFile(filepath.Join("../../../../examples/http-smoke", file))
		if err != nil {
			t.Fatal(err)
		}
		if file == "service.yaml" {
			context, _ := filepath.Abs("../../../../examples/http-smoke/app")
			b = []byte(strings.ReplaceAll(string(b), "./app", context))
		}
		if file == "scenario.yaml" {
			b = append([]byte("# user comment retained\n"), b...)
		}
		if err = os.WriteFile(filepath.Join(dir, file), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "scenario.yaml")
	add := func() error {
		return app.AddComponent(context.Background(), ComponentEditor{}, path, "cache", components.Templates()["redis"])
	}
	if err := add(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte("# user comment retained")) || !bytes.Contains(b, []byte("./service.yaml")) {
		t.Fatalf("lost comments/relative reference: %s", b)
	}
	if err := app.RemoveComponent(context.Background(), ComponentEditor{}, path, "cache"); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"unknown: true\n", "components: {}\n", "---\nkind: Scenario\n"} {
		before, _ := os.ReadFile(path)
		bad := append(append([]byte(nil), before...), []byte(invalid)...)
		os.WriteFile(path, bad, 0600)
		if err := add(); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(bad, after) {
			t.Fatal("invalid edit overwrote source")
		}
		os.WriteFile(path, before, 0600)
	}
}
