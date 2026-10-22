package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"gopkg.in/yaml.v3"
)

const runnerName = "localmock-runner"

type sandbox struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Subnet    string            `json:"subnet"`
	Addresses map[string]string `json:"addresses"`
}

func isolateService(s *composeService) {
	s.Networks = map[string]attachment{"sandbox": {}}
	// Docker resolves local service names first. Unknown names cannot reach host DNS.
	s.DNS = []string{"127.0.0.1"}
	s.CapDrop = []string{"NET_RAW", "NET_ADMIN"}
	s.Sysctls = map[string]string{"net.ipv6.conf.all.disable_ipv6": "1"}
}

func loadCompose(r domain.Run) (composeFile, error) {
	var c composeFile
	b, err := os.ReadFile(filepath.Join(r.Directory, "compose.yaml"))
	if err == nil {
		err = yaml.Unmarshal(b, &c)
	}
	return c, err
}
func saveCompose(r domain.Run, c composeFile) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.Directory, "compose.yaml"), b, 0600)
}
func loadSpec(r domain.Run) (domain.Spec, error) {
	var spec domain.Spec
	b, err := os.ReadFile(filepath.Join(r.Directory, "spec.json"))
	if err == nil {
		err = json.Unmarshal(b, &spec)
	}
	return spec, err
}

func (d Driver) prepareRunner(r domain.Run) error {
	c, err := loadCompose(r)
	if err != nil {
		return err
	}
	dir := filepath.Join(r.Directory, "build", runnerName)
	if err = prepareBinary(dir, d.Executable); err != nil {
		return err
	}
	s := composeService{Image: "localmock/" + r.ID + "-runner:local", Build: &composeBuild{Context: dir, Dockerfile: "Dockerfile"}, Command: []string{"runtime", "idle"},
		Labels:      map[string]string{"io.localmock.managed": "true", "io.localmock.run": r.ID, "io.localmock.role": "system"},
		Healthcheck: healthcheck{Test: []string{"CMD", "/localmock", "runtime", "health"}, Interval: "2s", Timeout: "2s", Retries: 5, StartPeriod: "1s"},
		Init:        true, Restart: "no", Memory: "128m", CPUs: 0.5, Pids: 64, Security: []string{"no-new-privileges:true"},
		Logging: logging{Driver: "json-file", Options: map[string]string{"max-size": "1m", "max-file": "1"}}}
	isolateService(&s)
	c.Services[runnerName] = s
	return saveCompose(r, c)
}

func (d Driver) createSandbox(ctx context.Context, r domain.Run) error {
	var version bytes.Buffer
	if err := d.command(ctx, &version, "version", "--format", "{{.Server.Version}}"); err != nil {
		return err
	}
	major, err := strconv.Atoi(strings.Split(strings.TrimSpace(version.String()), ".")[0])
	if err != nil || major < 28 {
		return fmt.Errorf("isolated networking requires Docker Engine 28 or newer")
	}
	name := r.Project + "-sandbox"
	var output bytes.Buffer
	if err = d.command(ctx, &output, "network", "create", "--driver", "bridge", "--internal",
		"--opt", "com.docker.network.bridge.gateway_mode_ipv4=isolated", "--opt", "com.docker.network.bridge.gateway_mode_ipv6=isolated",
		"--label", "io.localmock.managed=true", "--label", "io.localmock.run="+r.ID, name); err != nil {
		return err
	}
	n, err := d.inspectNetwork(ctx, name)
	if err != nil {
		return err
	}
	if err = n.check(r.ID); err != nil {
		return err
	}
	var prefix netip.Prefix
	for _, v := range n.IPAM.Config {
		candidate, e := netip.ParsePrefix(v.Subnet)
		if e == nil && candidate.Addr().Is4() {
			prefix = candidate
			break
		}
	}
	if !prefix.IsValid() || prefix.Bits() > 24 {
		return fmt.Errorf("Docker IPAM must allocate an IPv4 subnet with at least 256 addresses")
	}
	spec, err := loadSpec(r)
	if err != nil {
		return err
	}
	c, err := loadCompose(r)
	if err != nil {
		return err
	}
	snapshot := sandbox{ID: n.ID, Name: name, Subnet: prefix.String(), Addresses: map[string]string{}}
	used := map[int]bool{2: true}
	for _, workload := range Names(spec) {
		offset := spec.Workloads[workload].IPOffset
		if offset < 10 || offset > 249 || used[offset] {
			return fmt.Errorf("invalid or duplicate IP offset for %s", workload)
		}
		used[offset] = true
		address := prefix.Masked().Addr()
		for i := 0; i < offset; i++ {
			address = address.Next()
		}
		snapshot.Addresses[workload] = address.String()
	}
	address := prefix.Masked().Addr().Next().Next()
	snapshot.Addresses[runnerName] = address.String()
	for workload, ip := range snapshot.Addresses {
		s := c.Services[workload]
		s.Networks = map[string]attachment{"sandbox": {IPv4Address: ip}}
		c.Services[workload] = s
	}
	if err = writeJSON(filepath.Join(r.Directory, "network.json"), snapshot); err != nil {
		return err
	}
	return saveCompose(r, c)
}

type networkInspection struct {
	ID                   string `json:"Id"`
	Driver               string
	Internal, EnableIPv6 bool
	Options, Labels      map[string]string
	IPAM                 struct{ Config []struct{ Subnet string } }
}

func (n networkInspection) check(runID string) error {
	if n.Driver != "bridge" || !n.Internal || n.EnableIPv6 || n.Options["com.docker.network.bridge.gateway_mode_ipv4"] != "isolated" || n.Options["com.docker.network.bridge.gateway_mode_ipv6"] != "isolated" || n.Labels["io.localmock.run"] != runID || n.Labels["io.localmock.managed"] != "true" {
		return fmt.Errorf("sandbox network does not meet isolation policy")
	}
	return nil
}
func (d Driver) inspectNetwork(ctx context.Context, name string) (networkInspection, error) {
	var b bytes.Buffer
	var result []networkInspection
	err := d.command(ctx, &b, "network", "inspect", name)
	if err == nil {
		err = json.Unmarshal(b.Bytes(), &result)
	}
	if err != nil {
		return networkInspection{}, err
	}
	if len(result) != 1 {
		return networkInspection{}, fmt.Errorf("expected one sandbox network")
	}
	return result[0], nil
}
func (d Driver) auditSandbox(ctx context.Context, r domain.Run) error {
	n, err := d.inspectNetwork(ctx, r.Project+"-sandbox")
	if err != nil {
		return err
	}
	if err = n.check(r.ID); err != nil {
		return err
	}
	ids, err := d.ownedContainerIDs(ctx, r)
	if err != nil {
		return err
	}
	spec, err := loadSpec(r)
	if err != nil {
		return err
	}
	if len(ids) != len(spec.Workloads)+1 {
		return fmt.Errorf("sandbox container count differs from execution spec")
	}
	var b bytes.Buffer
	if err = d.command(ctx, &b, append([]string{"inspect"}, ids...)...); err != nil {
		return err
	}
	var containers []struct {
		HostConfig struct {
			Privileged   bool
			NetworkMode  string
			DNS          []string `json:"Dns"`
			CapDrop      []string
			Binds        []string
			Sysctls      map[string]string
			PortBindings map[string]json.RawMessage
		}
		NetworkSettings struct {
			Networks map[string]struct{ NetworkID string }
		}
	}
	if err = json.Unmarshal(b.Bytes(), &containers); err != nil {
		return err
	}
	for _, c := range containers {
		h := c.HostConfig
		dropped := func(cap string) bool {
			return slices.Contains(h.CapDrop, cap) || slices.Contains(h.CapDrop, "CAP_"+cap)
		}
		if h.Privileged || h.NetworkMode == "host" || len(h.Binds) > 0 || len(h.PortBindings) > 0 || len(c.NetworkSettings.Networks) != 1 || !slices.Equal(h.DNS, []string{"127.0.0.1"}) || !dropped("NET_RAW") || !dropped("NET_ADMIN") || h.Sysctls["net.ipv6.conf.all.disable_ipv6"] != "1" {
			return fmt.Errorf("container violates sandbox isolation policy")
		}
		for _, attached := range c.NetworkSettings.Networks {
			if attached.NetworkID != n.ID {
				return fmt.Errorf("container joined a different network")
			}
		}
	}
	return nil
}
