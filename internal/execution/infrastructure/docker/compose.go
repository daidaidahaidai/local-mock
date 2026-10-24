package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	Executable  string // optional static CLI path for embedding/integration tests
}
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Networks map[string]network        `yaml:"networks"`
}
type network struct {
	Name     string `yaml:"name"`
	External bool   `yaml:"external"`
}
type attachment struct {
	IPv4Address string `yaml:"ipv4_address,omitempty"`
}
type composeService struct {
	Image       string                `yaml:"image"`
	Build       *composeBuild         `yaml:"build,omitempty"`
	Command     []string              `yaml:"command,omitempty"`
	Environment map[string]string     `yaml:"environment,omitempty"`
	Ports       []port                `yaml:"ports,omitempty"`
	Labels      map[string]string     `yaml:"labels"`
	Healthcheck healthcheck           `yaml:"healthcheck"`
	Init        bool                  `yaml:"init"`
	Restart     string                `yaml:"restart"`
	Memory      string                `yaml:"mem_limit"`
	CPUs        float64               `yaml:"cpus"`
	Pids        int                   `yaml:"pids_limit"`
	Security    []string              `yaml:"security_opt"`
	Logging     logging               `yaml:"logging"`
	Networks    map[string]attachment `yaml:"networks"`
	DNS         []string              `yaml:"dns"`
	CapDrop     []string              `yaml:"cap_drop"`
	Sysctls     map[string]string     `yaml:"sysctls"`
	DependsOn   map[string]condition  `yaml:"depends_on,omitempty"`
}
type condition struct {
	Condition string `yaml:"condition"`
}
type composeBuild struct {
	Context    string            `yaml:"context"`
	Dockerfile string            `yaml:"dockerfile"`
	Args       map[string]string `yaml:"args,omitempty"`
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
	if spec.Network != "" && spec.Network != "isolated" {
		return fmt.Errorf("only isolated runtime networks are supported")
	}
	c := composeFile{Services: map[string]composeService{}, Networks: map[string]network{"sandbox": {Name: run.Project + "-sandbox", External: true}}}
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
		if name == "localmock-runner" || len(w.Publish) > 0 {
			return fmt.Errorf("reserved runner name or forbidden host port publishing: %s", name)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		s := composeService{Image: literal(w.Image), Command: literals(w.Command), Environment: map[string]string{}, Labels: map[string]string{"io.localmock.managed": "true", "io.localmock.run": run.ID, "io.localmock.service": w.Service, "io.localmock.role": w.Role}, Init: true, Restart: "no", Memory: "512m", CPUs: 1, Pids: 256, Security: []string{"no-new-privileges:true"}, Logging: logging{Driver: "json-file", Options: map[string]string{"max-size": "10m", "max-file": "3"}}}
		isolateService(&s)
		if w.MemoryMB > 0 {
			s.Memory = strconv.Itoa(w.MemoryMB) + "m"
		}
		if w.Role != "infrastructure" {
			s.DependsOn = map[string]condition{}
			for dependency, other := range spec.Workloads {
				if other.Role == "infrastructure" {
					s.DependsOn[dependency] = condition{Condition: "service_healthy"}
				}
			}
		}
		for k, v := range w.Env {
			s.Environment[k] = literal(v)
		}
		if w.Build != nil {
			buildDir := filepath.Join(run.Directory, "build", name)
			if err := copyTree(ctx, w.Build.Context, buildDir); err != nil {
				return fmt.Errorf("snapshot build %s: %w", name, err)
			}
			s.Build = &composeBuild{Context: buildDir, Dockerfile: w.Build.Dockerfile, Args: map[string]string{}}
			for key, value := range w.Build.Args {
				s.Build.Args[key] = literal(value)
			}
			s.Image = "localmock/" + run.ID + "-" + name + ":local"
		}
		if len(w.MockBundle) > 0 {
			buildDir := filepath.Join(run.Directory, "build", name)
			if err := prepareMock(buildDir, w.MockBundle, d.Executable); err != nil {
				return err
			}
			s.Build = &composeBuild{Context: buildDir, Dockerfile: "Dockerfile"}
			s.Image = "localmock/" + run.ID + "-" + name + ":local"
		}
		s.Healthcheck = healthcheck{Test: append([]string{"CMD"}, literals(w.Readiness)...), Interval: "2s", Timeout: "3s", Retries: 60, StartPeriod: "5s"}
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
	if err := d.prepareRunner(r); err != nil {
		return err
	}
	if err := d.compose(ctx, r, d.Progress, "build"); err != nil {
		return err
	}
	// Fetch images before any runtime starts. Workloads never receive an egress network.
	if err := d.compose(ctx, r, d.Progress, "pull", "--ignore-buildable", "--policy", "missing"); err != nil {
		return err
	}
	if err := d.createSandbox(ctx, r); err != nil {
		return err
	}
	wait := d.WaitSeconds
	if wait <= 0 {
		wait = 90
	}
	spec, err := loadSpec(r)
	if err != nil {
		return err
	}
	if hasInitialization(spec) {
		infra := []string{runnerName}
		for _, name := range Names(spec) {
			if spec.Workloads[name].Role == "infrastructure" {
				infra = append(infra, name)
			}
		}
		args := append([]string{"up", "--detach", "--wait", "--wait-timeout", strconv.Itoa(wait), "--no-deps"}, infra...)
		if err = d.compose(ctx, r, d.Progress, args...); err != nil {
			return err
		}
		if err = d.auditSandboxWorkloads(ctx, r, infra); err != nil {
			return err
		}
		if err = d.initializeComponents(ctx, r, spec); err != nil {
			return err
		}
	}
	if err := d.compose(ctx, r, d.Progress, "up", "--detach", "--wait", "--wait-timeout", strconv.Itoa(wait)); err != nil {
		return err
	}
	if err := d.auditSandbox(ctx, r); err != nil {
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
	return d.command(ctx, d.Progress, append([]string{"stop", "-t", "10"}, ids...)...)
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
	if err = d.command(ctx, &b, append([]string{"inspect", "--format", `{"id":{{json .Id}},"deployment":{{json (index .Config.Labels "com.docker.compose.service")}},"state":{{json .State.Status}},"health":{{if .State.Health}}{{json .State.Health.Status}}{{else}}"none"{{end}},"ports":{{json .NetworkSettings.Ports}},"role":{{json (index .Config.Labels "io.localmock.role")}},"networks":{{json .NetworkSettings.Networks}}}`}, ids...)...); err != nil {
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
	return d.commandInput(ctx, nil, w, args...)
}
func (d Driver) commandInput(ctx context.Context, input io.Reader, w io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = input
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

func prepareMock(dir string, bundle []byte, executable string) error {
	if err := prepareBinary(dir, executable); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), bundle, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nCOPY localmock /localmock\nCOPY bundle.json /bundle.json\nUSER 65532:65532\nENTRYPOINT [\"/localmock\"]\n"), 0600)
}

func prepareBinary(dir, executable string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("network-local tooling and mocks require Linux")
	}
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	f, err := elf.Open(executable)
	if err != nil {
		return err
	}
	libraries, err := f.ImportedLibraries()
	f.Close()
	if err != nil {
		return err
	}
	if len(libraries) > 0 {
		return fmt.Errorf("network-local tooling and mocks require a static localmock binary; build with CGO_ENABLED=0")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	in, err := os.Open(executable)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(dir, "localmock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nCOPY localmock /localmock\nUSER 65532:65532\nENTRYPOINT [\"/localmock\"]\n"), 0600)
}
