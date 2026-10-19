package docker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"gopkg.in/yaml.v3"
)

func TestPrepareQuotesEnvironmentAndSnapshotsBuild(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "Dockerfile"), []byte("FROM scratch\n"), 0600)
	run := domain.Run{ID: "012345678901234567890123", Directory: t.TempDir()}
	spec := domain.Spec{Workloads: map[string]domain.Workload{"app": {Build: &domain.BuildSpec{Context: src, Dockerfile: "Dockerfile"}, Env: map[string]string{"TOKEN": "literal$VALUE"}, Readiness: []string{"/health"}, Publish: []domain.PublishedPort{{Target: 8080, Published: 0}}}}}
	if err := (Driver{}).Prepare(context.Background(), run, spec); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(run.Directory, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var c composeFile
	if err = yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	s := c.Services["app"]
	if s.Environment["TOKEN"] != "literal$$VALUE" || s.Ports[0].HostIP != "127.0.0.1" || s.Labels["io.localmock.run"] != run.ID {
		t.Fatalf("%+v", s)
	}
	os.WriteFile(filepath.Join(src, "Dockerfile"), []byte("changed"), 0600)
	snapshot, err := os.ReadFile(filepath.Join(s.Build.Context, "Dockerfile"))
	if err != nil || string(snapshot) != "FROM scratch\n" {
		t.Fatalf("%s %v", snapshot, err)
	}
}
func TestRejectsChangedInputsAndRecursiveSnapshot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input")
	os.WriteFile(file, []byte("new"), 0600)
	spec := domain.Spec{Inputs: map[string]string{file: fmt.Sprintf("%x", sha256.Sum256([]byte("old")))}}
	if err := (Driver{}).Prepare(context.Background(), domain.Run{Directory: t.TempDir()}, spec); err == nil {
		t.Fatal("accepted changed input")
	}
	src := t.TempDir()
	if err := copyTree(context.Background(), src, filepath.Join(src, "nested")); err == nil {
		t.Fatal("accepted recursive snapshot")
	}
}
func TestSnapshotRejectsSymlinks(t *testing.T) {
	src := t.TempDir()
	os.Symlink("/etc/passwd", filepath.Join(src, "link"))
	err := copyTree(context.Background(), src, filepath.Join(t.TempDir(), "build"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatal(err)
	}
}
