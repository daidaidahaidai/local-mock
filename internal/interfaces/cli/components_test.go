package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
)

func TestComponentSelectionCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	var output bytes.Buffer
	c := CLI{Out: &output, Err: &output}
	run := func(want int, args ...string) {
		t.Helper()
		output.Reset()
		if got := c.Run(context.Background(), append([]string{"components"}, args...)); got != want {
			t.Fatalf("exit=%d: %s", got, &output)
		}
	}
	run(0, "add", "redis", "--name", "cache", "-f", path)
	run(0, "add", "-f", path, "--name", "sessions", "redis")
	run(0, "add", "mysql", "-f", path)
	doc, err := loader.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Scenario.Components) != 3 || doc.Scenario.Components["cache"].IPOffset == doc.Scenario.Components["sessions"].IPOffset {
		t.Fatal("selection or IP allocation failed")
	}
	before, _ := os.ReadFile(path)
	run(2, "add", "redis", "--name", "cache", "-f", path)
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed addition changed file")
	}
	run(0, "remove", "sessions", "-f", path)
	// A component reference must prevent removal, with no partial edit.
	b, _ := os.ReadFile(path)
	b = []byte(strings.Replace(string(b), "MYSQL_DATABASE: localmock", "MYSQL_DATABASE: localmock\n      CACHE: '${component.cache.redis.address}'", 1))
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(path); err != nil {
		t.Fatal(err)
	}
	run(2, "remove", "cache", "-f", path)
	after, _ = os.ReadFile(path)
	if !bytes.Equal(b, after) || !strings.Contains(output.String(), "INVALID_COMPONENT_REFERENCE") && !strings.Contains(output.String(), "not selected") {
		t.Fatalf("unexpected removal: %s", &output)
	}
}
