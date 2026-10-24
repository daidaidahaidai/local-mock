package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"github.com/daidaidahaidai/local-mock/internal/catalog/infrastructure/components"
	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
)

func TestSeededDatastoresBusinessReadsAndWrites(t *testing.T) {
	if os.Getenv("LOCALMOCK_COMPONENT_TEST") != "1" {
		t.Skip("set LOCALMOCK_COMPONENT_TEST=1")
	}
	h := setup(t)
	doc, err := loader.Load(filepath.Join(h.root, "examples/datastores/scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Use the development host's reachable proxy only for the build stage.
	svc := doc.Services["orders"]
	impl := svc.Implementations["real"]
	impl.Build.Args = map[string]string{"GOPROXY": "https://goproxy.cn,direct"}
	svc.Implementations["real"] = impl
	doc.Services["orders"] = svc
	file, err := loader.Export(doc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r := h.start(file)
		b, err := h.command("probe", "--deployment", "orders", "--interface", "api", "--path", "/exercise", r.ID)
		if err != nil {
			t.Fatal(err)
		}
		var result struct{ Code, Body string }
		if err = json.Unmarshal(b, &result); err != nil || result.Code != "200" {
			t.Fatalf("probe: %s %v", b, err)
		}
		var response struct {
			ID       int    `json:"order_id"`
			Product  string `json:"product"`
			Cached   string `json:"cached_product"`
			Greeting string `json:"greeting"`
		}
		if err = json.Unmarshal([]byte(result.Body), &response); err != nil || response.ID != 1 || response.Product != "local coffee" || response.Cached != response.Product || response.Greeting != "hello from fixtures" {
			t.Fatalf("wrong data or leaked prior Run: %s %v", result.Body, err)
		}
		if got := h.execContainer(r.ID, "database", "mysql", "-h127.0.0.1", "-uroot", "--skip-column-names", "localmock", "-e", "SELECT COUNT(*) FROM orders"); got != "1" {
			t.Fatal(got)
		}
		if got := h.execContainer(r.ID, "cache", "redis-cli", "GET", "order:1"); got != "local coffee" {
			t.Fatal(got)
		}
		if got := h.execContainer(r.ID, "cache", "redis-cli", "HGET", "fixture:customer", "name"); got != "Alice" {
			t.Fatal(got)
		}
		ttl, err := strconv.Atoi(h.execContainer(r.ID, "cache", "redis-cli", "TTL", "fixture:session"))
		if err != nil || ttl <= 0 || ttl > 3600 {
			t.Fatalf("TTL %d %v", ttl, err)
		}
		journal, err := os.ReadFile(filepath.Join(h.state, "runs", r.ID, "initialization.jsonl"))
		if err != nil || bytes.Count(journal, []byte(`"status":"succeeded"`)) != 5 {
			t.Fatalf("initialization: %s %v", journal, err)
		}
		if _, err = h.command("down", r.ID); err != nil {
			t.Fatal(err)
		}
		h.assertRemoved(r.ID)
		if _, err = os.Stat(filepath.Join(h.state, "runs", r.ID, "initialization.jsonl")); err != nil {
			t.Fatal("journal lost after cleanup")
		}
	}
}

func TestInitializationFailureBlocksBusinessAndCleansRun(t *testing.T) {
	if os.Getenv("LOCALMOCK_COMPONENT_TEST") != "1" {
		t.Skip("set LOCALMOCK_COMPONENT_TEST=1")
	}
	for _, tc := range []struct {
		name, template string
		step           catalog.Initialization
	}{
		{"redis-error", "redis", catalog.Initialization{Name: "bad", Command: []string{"redis-cli", "-e", "NOT_A_COMMAND"}}},
		{"sql-error", "mysql", catalog.Initialization{Name: "bad", Command: []string{"mysql", "-h127.0.0.1", "-uroot", "-e", "THIS IS NOT SQL"}}},
		{"timeout", "redis", catalog.Initialization{Name: "bad", Command: []string{"sh", "-c", "sleep 30"}, TimeoutSeconds: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setup(t)
			doc, err := loader.Load(filepath.Join(h.root, "examples/http-smoke/scenario.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			c := components.Templates()[tc.template]
			c.Initialize = []catalog.Initialization{tc.step, {Name: "must-not-run", Command: []string{"sh", "-c", "exit 0"}}}
			doc.Scenario.Components = map[string]catalog.Component{"store": c}
			file, err := loader.Export(doc, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.command("up", "-f", file); err == nil {
				t.Fatal("initialization failure accepted")
			}
			rows := h.list()
			if len(rows) != 1 || rows[0].Run.State != "failed" {
				t.Fatalf("unexpected runs %+v", rows)
			}
			r := rows[0].Run
			journal, err := os.ReadFile(filepath.Join(h.state, "runs", r.ID, "initialization.jsonl"))
			if err != nil || !bytes.Contains(journal, []byte(`"status":"failed"`)) || bytes.Contains(journal, []byte("must-not-run")) {
				t.Fatalf("wrong initialization journal: %s %v", journal, err)
			}
			startup, err := os.ReadFile(filepath.Join(h.state, "runs", r.ID, "startup.log"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(startup), "-caller-1") || strings.Contains(string(startup), "-dependency-1") {
				t.Fatal("business created before successful initialization")
			}
			h.assertRemoved(r.ID)
		})
	}
}

func TestInterruptDuringInitializationCleansRun(t *testing.T) {
	if os.Getenv("LOCALMOCK_COMPONENT_TEST") != "1" {
		t.Skip("set LOCALMOCK_COMPONENT_TEST=1")
	}
	h := setup(t)
	doc, err := loader.Load(filepath.Join(h.root, "examples/infrastructure/scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c := components.Templates()["redis"]
	c.Initialize = []catalog.Initialization{{Name: "wait", Command: []string{"sh", "-c", "sleep 60"}, TimeoutSeconds: 90}}
	doc.Scenario.Components = map[string]catalog.Component{"cache": c}
	file, err := loader.Export(doc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bin, "up", "--state", h.state, "-f", file)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Always reap the process before harness cleanup, including failed waits.
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	deadline := time.Now().Add(50 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(h.state, "runs", "*", "initialization.jsonl"))
		if len(files) == 1 {
			b, _ := os.ReadFile(files[0])
			started = bytes.Contains(b, []byte(`"status":"started"`))
		}
		if started {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !started {
		t.Fatal("initialization did not start")
	}
	if err = cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil || cmd.ProcessState.ExitCode() != 130 {
		t.Fatalf("cancel: %s %v", &output, err)
	}
	rows := h.list()
	if len(rows) != 1 || rows[0].Run.State != "failed" {
		t.Fatalf("cancel state: %+v", rows)
	}
	h.assertRemoved(rows[0].Run.ID)
}
