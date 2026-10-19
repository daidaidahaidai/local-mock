package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"gopkg.in/yaml.v3"
)

type Driver struct {
	Progress    io.Writer
	WaitSeconds int
}
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Networks map[string]network        `yaml:"networks"`
}
type network struct {
	Labels map[string]string `yaml:"labels"`
}
type composeService struct {
	Image       string            `yaml:"image"`
	Build       *composeBuild     `yaml:"build,omitempty"`
	Command     []string          `yaml:"command,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Ports       []port            `yaml:"ports,omitempty"`
	Labels      map[string]string `yaml:"labels"`
	Healthcheck healthcheck       `yaml:"healthcheck"`
	Init        bool              `yaml:"init"`
	Restart     string            `yaml:"restart"`
	Memory      string            `yaml:"mem_limit"`
	CPUs        float64           `yaml:"cpus"`
	Pids        int               `yaml:"pids_limit"`
	Security    []string          `yaml:"security_opt"`
	Logging     logging           `yaml:"logging"`
}
type composeBuild struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile"`
}
type port struct {
	Target    int    `yaml:"target"`
	Published string `yaml:"published"`
	HostIP    string `yaml:"host_ip"`
}
type healthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval"`
	Timeout     string   `yaml:"timeout"`
	Retries     int      `yaml:"retries"`
	StartPeriod string   `yaml:"start_period"`
}
type logging struct {
	Driver  string            `yaml:"driver"`
	Options map[string]string `yaml:"options"`
}

func (d Driver) Prepare(ctx context.Context, run domain.Run, spec domain.Spec) error {
	// Verify loaded files have not changed before producing executable artifacts.
	for path, hash := range spec.Inputs {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(content)) != hash {
			return fmt.Errorf("input changed since validation: %s", path)
		}
	}
	if err := writeJSON(filepath.Join(run.Directory, "spec.json"), spec); err != nil {
		return err
	}
	labels := map[string]string{"io.localmock.managed": "true", "io.localmock.run": run.ID}
	c := composeFile{Services: map[string]composeService{}, Networks: map[string]network{"default": {Labels: labels}}}
	inputDir := filepath.Join(run.Directory, "inputs")
	if err := os.MkdirAll(inputDir, 0700); err != nil {
		return err
	}
	for path, hash := range spec.Inputs {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != hash {
			return fmt.Errorf("input changed while snapshotting: %s", path)
		}
		if err = os.WriteFile(filepath.Join(inputDir, hash), b, 0600); err != nil {
			return err
		}
	}
	for name, w := range spec.Workloads {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := composeService{Image: literal(w.Image), Command: literals(w.Command), Environment: map[string]string{}, Labels: map[string]string{"io.localmock.managed": "true", "io.localmock.run": run.ID, "io.localmock.service": w.Service, "io.localmock.role": w.Role}, Init: true, Restart: "no", Memory: "512m", CPUs: 1, Pids: 256, Security: []string{"no-new-privileges:true"}, Logging: logging{Driver: "json-file", Options: map[string]string{"max-size": "10m", "max-file": "3"}}}
		for k, v := range w.Env {
			s.Environment[k] = literal(v)
		}
		if w.Build != nil {
			buildDir := filepath.Join(run.Directory, "build", name)
			if err := copyTree(ctx, w.Build.Context, buildDir); err != nil {
				return fmt.Errorf("snapshot build %s: %w", name, err)
			}
			s.Build = &composeBuild{Context: buildDir, Dockerfile: w.Build.Dockerfile}
			s.Image = "localmock/" + run.ID + "-" + name + ":local"
		}
		for _, p := range w.Publish {
			s.Ports = append(s.Ports, port{Target: p.Target, Published: strconv.Itoa(p.Published), HostIP: "127.0.0.1"})
		}
		s.Healthcheck = healthcheck{Test: append([]string{"CMD"}, literals(w.Readiness)...), Interval: "1s", Timeout: "3s", Retries: 30, StartPeriod: "2s"}
		c.Services[name] = s
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(run.Directory, "compose.yaml"), b, 0600)
}
func (d Driver) Up(ctx context.Context, r domain.Run) error {
	buildLog, err := os.OpenFile(filepath.Join(r.Directory, "startup.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer buildLog.Close()
	if d.Progress == nil {
		d.Progress = buildLog
	} else {
		d.Progress = io.MultiWriter(d.Progress, buildLog)
	}
	if err := d.compose(ctx, r, d.Progress, "build"); err != nil {
		return err
	}
	wait := d.WaitSeconds
	if wait <= 0 {
		wait = 90
	}
	if err := d.compose(ctx, r, d.Progress, "up", "--detach", "--wait", "--wait-timeout", strconv.Itoa(wait)); err != nil {
		return err
	}
	ids, err := d.ownedContainerIDs(ctx, r)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		var b bytes.Buffer
		if err = d.command(ctx, &b, append([]string{"inspect", "--format", "{{json .}}"}, ids...)...); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(r.Directory, "containers.jsonl"), b.Bytes(), 0600); err != nil {
			return err
		}
	}
	return nil
}
func (d Driver) Stop(ctx context.Context, r domain.Run) error {
	ids, err := d.ownedContainerIDs(ctx, r)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return d.command(ctx, d.Progress, append([]string{"stop", "--time", "10"}, ids...)...)
}
func (d Driver) Down(ctx context.Context, r domain.Run) error {
	// Remove only explicitly labelled resources, even when compose.yaml is absent or edited.
	ids, err := d.ownedContainerIDs(ctx, r)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		if err = d.command(ctx, d.Progress, append([]string{"rm", "--force", "--volumes"}, ids...)...); err != nil {
			return err
		}
	}
	for _, kind := range []string{"network", "volume"} {
		var b bytes.Buffer
		if err = d.command(ctx, &b, kind, "ls", "--quiet", "--filter", "label=io.localmock.managed=true", "--filter", "label=io.localmock.run="+r.ID); err != nil {
			return err
		}
		resources := strings.Fields(b.String())
		if len(resources) > 0 {
			if err = d.command(ctx, d.Progress, append([]string{kind, "rm"}, resources...)...); err != nil {
				return err
			}
		}
	}
	return nil
}
func (d Driver) Status(ctx context.Context, r domain.Run) (json.RawMessage, error) {
	ids, err := d.ownedContainerIDs(ctx, r)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return json.RawMessage("[]"), nil
	}
	var b bytes.Buffer
	if err = d.command(ctx, &b, append([]string{"inspect", "--format", `{"id":{{json .Id}},"deployment":{{json (index .Config.Labels "com.docker.compose.service")}},"state":{{json .State.Status}},"health":{{if .State.Health}}{{json .State.Health.Status}}{{else}}"none"{{end}},"ports":{{json .NetworkSettings.Ports}}}`}, ids...)...); err != nil {
		return nil, err
	}
	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	return json.RawMessage(append(append([]byte("["), bytes.Join(lines, []byte(","))...), ']')), nil
}
func (d Driver) Logs(ctx context.Context, r domain.Run, w io.Writer) error {
	ids, err := d.ownedContainerIDs(ctx, r)
	if err != nil {
		return err
	}
	for _, id := range ids {
		fmt.Fprintf(w, "--- container %s ---\n", id)
		if err = d.command(ctx, w, "logs", "--timestamps", "--tail", "1000", id); err != nil {
			return err
		}
	}
	return nil
}
func (d Driver) ownedContainerIDs(ctx context.Context, r domain.Run) ([]string, error) {
	if !regexp.MustCompile(`^[0-9a-f]{24}$`).MatchString(r.ID) {
		return nil, fmt.Errorf("invalid run ID")
	}
	var b bytes.Buffer
	err := d.command(ctx, &b, "ps", "--all", "--quiet", "--filter", "label=io.localmock.managed=true", "--filter", "label=io.localmock.run="+r.ID)
	return strings.Fields(b.String()), err
}
func (d Driver) compose(ctx context.Context, r domain.Run, w io.Writer, args ...string) error {
	base := []string{"compose", "--project-name", r.Project, "--file", filepath.Join(r.Directory, "compose.yaml"), "--env-file", "/dev/null"}
	return d.command(ctx, w, append(base, args...)...)
}
func (d Driver) command(ctx context.Context, w io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 3 * time.Second
	// No model credentials or incidental Compose variables enter child environments.
	for _, key := range []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "SSH_AUTH_SOCK", "XDG_RUNTIME_DIR"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	if w == nil {
		w = io.Discard
	}
	cmd.Stdout = w
	cmd.Stderr = d.Progress
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}
	if len(args) > 0 && args[0] == "logs" {
		cmd.Stderr = w
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", args[0], err)
	}
	return nil
}
func literal(s string) string { return strings.ReplaceAll(s, "$", "$$") }
func literals(s []string) []string {
	r := make([]string, len(s))
	for i, v := range s {
		r[i] = literal(v)
	}
	return r
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
func copyTree(ctx context.Context, src, dst string) error {
	if rel, err := filepath.Rel(src, dst); err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("state directory must be outside the source build context")
	}
	var total int64
	return filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if e.IsDir() && (e.Name() == ".git" || e.Name() == ".localmock") {
			return filepath.SkipDir
		}
		if strings.HasPrefix(e.Name(), "._") || e.Name() == ".DS_Store" {
			return nil
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks in build contexts are not supported: %s", rel)
		}
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular build input: %s", rel)
		}
		total += info.Size()
		if total > 256<<20 {
			return fmt.Errorf("build context exceeds 256 MiB")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0700|0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// Stable deployment ordering is useful to callers presenting maps.
func Names(spec domain.Spec) []string {
	names := make([]string, 0, len(spec.Workloads))
	for name := range spec.Workloads {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
