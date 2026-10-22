package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type run struct {
	ID    string `json:"id"`
	State string `json:"state"`
}
type observed struct {
	Run        run `json:"run"`
	Containers []struct {
		State      string `json:"state"`
		Deployment string `json:"deployment"`
		Ports      map[string][]struct {
			HostPort string `json:"HostPort"`
		} `json:"ports"`
	} `json:"containers"`
}
type harness struct {
	t                *testing.T
	bin, root, state string
}

func setup(t *testing.T) *harness {
	t.Helper()
	if os.Getenv("LOCALMOCK_DOCKER_TEST") != "1" {
		t.Skip("set LOCALMOCK_DOCKER_TEST=1 on a Docker host")
	}
	root := os.Getenv("LOCALMOCK_REPO_ROOT")
	if root == "" {
		root, _ = os.Getwd()
		for {
			if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(root)
			if parent == root {
				t.Fatal("cannot find repository root")
			}
			root = parent
		}
	}
	bin := os.Getenv("LOCALMOCK_BIN")
	if bin == "" {
		bin = filepath.Join(root, "bin", "localmock")
	}
	h := &harness{t: t, bin: bin, root: root, state: t.TempDir()}
	t.Cleanup(func() {
		for _, r := range h.list() {
			_, _ = h.command("down", r.Run.ID)
		}
	})
	return h
}
func (h *harness) command(command string, args ...string) ([]byte, error) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	argv := append([]string{command, "--state", h.state, "--json"}, args...)
	cmd := exec.CommandContext(ctx, h.bin, argv...)
	cmd.Dir = h.root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		h.t.Logf("%s: %s", command, stderr.String())
	}
	return b, err
}
func (h *harness) list() []observed {
	h.t.Helper()
	b, err := h.command("ps")
	if err != nil {
		h.t.Errorf("ps: %v", err)
		return nil
	}
	var rows []observed
	if err = json.Unmarshal(b, &rows); err != nil {
		h.t.Errorf("ps JSON: %s %v", b, err)
	}
	return rows
}
func (h *harness) start(file string) run {
	h.t.Helper()
	b, err := h.command("up", "-f", file)
	if err != nil {
		h.t.Fatal(err)
	}
	var r run
	if err = json.Unmarshal(b, &r); err != nil {
		h.t.Fatal(err)
	}
	if r.State != "ready" {
		h.t.Fatalf("%+v", r)
	}
	return r
}
func (h *harness) request(id string) {
	h.t.Helper()
	b, err := h.command("probe", "--deployment", "caller", "--interface", "api", "--path", "/", id)
	if err != nil {
		h.t.Fatal(err)
	}
	var result struct{ Code, Body string }
	if err = json.Unmarshal(b, &result); err != nil || result.Code != "200" || !strings.Contains(result.Body, "hello from mock") {
		h.t.Fatalf("probe: %s %v", b, err)
	}
}
func (h *harness) assertRemoved(id string) {
	h.t.Helper()
	for _, args := range [][]string{{"ps", "-aq"}, {"network", "ls", "-q"}, {"volume", "ls", "-q"}} {
		args = append(args, "--filter", "label=io.localmock.run="+id)
		b, err := exec.Command("docker", args...).Output()
		if err != nil || len(bytes.TrimSpace(b)) != 0 {
			h.t.Fatalf("resources remain: %s %v", b, err)
		}
	}
}
func TestTwoRunsAreIsolatedAndCleanupIsIdempotent(t *testing.T) {
	h := setup(t)
	file := filepath.Join(h.root, "examples/http-smoke/scenario.yaml")
	a := h.start(file)
	b := h.start(file)
	h.request(a.ID)
	h.request(b.ID)
	if _, err := h.command("down", a.ID); err != nil {
		t.Fatal(err)
	}
	h.assertRemoved(a.ID)
	h.request(b.ID)
	if _, err := h.command("stop", b.ID); err != nil {
		t.Fatal(err)
	}
	for _, r := range h.list() {
		if r.Run.ID == b.ID {
			if r.Run.State != "stopped" {
				t.Fatal("not stopped")
			}
			for _, c := range r.Containers {
				if c.State != "exited" {
					t.Fatalf("container state %s", c.State)
				}
			}
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := h.command("down", b.ID); err != nil {
			t.Fatal(err)
		}
	}
	h.assertRemoved(b.ID)
	if _, err := h.command("logs", a.ID); err != nil {
		t.Fatal("logs were not retained:", err)
	}
}
func (h *harness) unhealthyScenario() string {
	h.t.Helper()
	dest := filepath.Join(h.t.TempDir(), "fixture")
	source := filepath.Join(h.root, "examples/http-smoke")
	err := filepath.WalkDir(source, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		target := filepath.Join(dest, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rel == "service.yaml" {
			b = bytes.ReplaceAll(b, []byte("[/app, health]"), []byte("[/does-not-exist]"))
		}
		return os.WriteFile(target, b, 0600)
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return filepath.Join(dest, "scenario.yaml")
}
func TestStartupTimeoutCleansPartialResources(t *testing.T) {
	h := setup(t)
	_, err := h.command("up", "-f", h.unhealthyScenario(), "--timeout", "8s")
	if err == nil {
		t.Fatal("unhealthy start succeeded")
	}
	rows := h.list()
	if len(rows) != 1 || rows[0].Run.State != "failed" {
		t.Fatalf("%+v", rows)
	}
	h.assertRemoved(rows[0].Run.ID)
}
func TestInterruptDuringStartupCleansResources(t *testing.T) {
	h := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bin, "up", "--state", h.state, "-f", h.unhealthyScenario(), "--json")
	cmd.Dir = h.root
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var id string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		rows := h.list()
		if len(rows) == 1 {
			id = rows[0].Run.ID
			b, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=io.localmock.run="+id).Output()
			if strings.TrimSpace(string(b)) != "" {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if id == "" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("run not created")
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 130 {
		t.Fatalf("expected interrupt exit 130, got %v: %s", err, output.String())
	}
	h.assertRemoved(id)
}
