package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	catalog "github.com/daidaidahaidai/local-mock/internal/catalog/domain"
	"github.com/daidaidahaidai/local-mock/internal/catalog/infrastructure/components"
	execution "github.com/daidaidahaidai/local-mock/internal/execution/domain"
	scenario "github.com/daidaidahaidai/local-mock/internal/scenario/domain"
	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
)

func (h *harness) container(id, name string) string {
	h.t.Helper()
	b, err := exec.Command("docker", "ps", "-q", "--filter", "label=io.localmock.run="+id, "--filter", "label=com.docker.compose.service="+name).Output()
	if err != nil || len(strings.Fields(string(b))) != 1 {
		h.t.Fatalf("container %s: %s %v", name, b, err)
	}
	return strings.TrimSpace(string(b))
}
func (h *harness) execContainer(id, name string, args ...string) string {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "docker", append([]string{"exec", h.container(id, name)}, args...)...).CombinedOutput()
	if err != nil {
		h.t.Fatalf("container command failed: %s %v", b, err)
	}
	return strings.TrimSpace(string(b))
}
func (h *harness) rawProbe(id string, call execution.ProbeCall) execution.ProbeResult {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, _ := json.Marshal(call)
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", h.container(id, "localmock-runner"), "/localmock", "runtime", "probe")
	cmd.Stdin = bytes.NewReader(b)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("sandbox probe: %s %v", out, err)
	}
	var result execution.ProbeResult
	if err = json.Unmarshal(out, &result); err != nil {
		h.t.Fatalf("probe output %s: %v", out, err)
	}
	return result
}
func (h *harness) addresses(id string) map[string]string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.state, "runs", id, "network.json"))
	if err != nil {
		h.t.Fatal(err)
	}
	var n struct{ Addresses map[string]string }
	if err = json.Unmarshal(b, &n); err != nil {
		h.t.Fatal(err)
	}
	return n.Addresses
}

func TestUserSelectedInfrastructureLifecycle(t *testing.T) {
	if os.Getenv("LOCALMOCK_COMPONENT_TEST") != "1" {
		t.Skip("set LOCALMOCK_COMPONENT_TEST=1 to run the Nacos/MySQL/Redis images")
	}
	h := setup(t)
	doc, err := loader.Load(filepath.Join(h.root, "examples/http-smoke/scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc.Scenario.Components = components.Templates()
	// An arbitrary user name/image follows the same path as a supplied template.
	doc.Scenario.Components["custom-cache"] = doc.Scenario.Components["redis"]
	delete(doc.Scenario.Components, "redis")
	d := doc.Scenario.Deployments["caller"]
	d.Env = map[string]string{"CACHE_ADDRESS": "${component.custom-cache.redis.address}"}
	doc.Scenario.Deployments["caller"] = d
	file, err := loader.Export(doc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := h.start(file)
	h.request(r.ID)
	addresses := h.addresses(r.ID)
	for name, suffix := range map[string]string{"nacos": ".10", "mysql": ".30", "custom-cache": ".40"} {
		if !strings.HasSuffix(addresses[name], suffix) {
			t.Fatalf("wrong IP slot %s: %v", name, addresses)
		}
	}
	h.verifyDatastores(r.ID, "custom-cache")
	base := "http://127.0.0.1:8848/nacos/v1/"
	if got := h.execContainer(r.ID, "nacos", "curl", "-fsS", "-X", "POST", base+"ns/instance?serviceName=localmock-payment&ip="+addresses["dependency"]+"&port=8080&ephemeral=false"); got != "ok" {
		t.Fatal(got)
	}
	if got := h.execContainer(r.ID, "nacos", "curl", "-fsS", base+"ns/instance/list?serviceName=localmock-payment"); !strings.Contains(got, addresses["dependency"]) {
		t.Fatal(got)
	}
	if got := h.execContainer(r.ID, "nacos", "curl", "-fsS", "-X", "POST", base+"cs/configs", "--data", "dataId=localmock-test&group=LOCAL&content=local-only"); got != "true" {
		t.Fatal(got)
	}
	// Nacos publishes a change event before its serving cache is refreshed.
	// Poll only the read; never retry the configuration write.
	h.awaitConfig(r.ID, base+"cs/configs?dataId=localmock-test&group=LOCAL", "local-only")
	if _, err = h.command("stop", r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.command("down", r.ID); err != nil {
		t.Fatal(err)
	}
	h.assertRemoved(r.ID)
}

func (h *harness) awaitConfig(id, url, want string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	container := h.container(id, "nacos")
	var last string
	for {
		b, err := exec.CommandContext(ctx, "docker", "exec", container, "curl", "-sS", "--max-time", "2", "--write-out", "\n%{http_code}", url).CombinedOutput()
		last = string(b)
		if err != nil {
			h.t.Fatalf("config read failed: %s %v", last, err)
		}
		if last == want+"\n200" {
			return
		}
		if !strings.HasSuffix(last, "\n404") {
			h.t.Fatalf("unexpected config response: %s", last)
		}
		select {
		case <-ctx.Done():
			h.t.Fatalf("configuration did not become visible: %s", last)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (h *harness) verifyDatastores(id, cache string) {
	h.t.Helper()
	if got := h.execContainer(id, cache, "redis-cli", "SET", "localmock-test", "selected"); got != "OK" {
		h.t.Fatal(got)
	}
	if got := h.execContainer(id, cache, "redis-cli", "GET", "localmock-test"); got != "selected" {
		h.t.Fatal(got)
	}
	if got := h.execContainer(id, "mysql", "mysql", "-h127.0.0.1", "-uroot", "--skip-column-names", "localmock", "-e", "CREATE TABLE fixture (value INT); INSERT INTO fixture VALUES (42); SELECT value FROM fixture;"); got != "42" {
		h.t.Fatal(got)
	}
}

func TestStandaloneDatastoresAndVolumeCleanup(t *testing.T) {
	if os.Getenv("LOCALMOCK_COMPONENT_TEST") != "1" {
		t.Skip("set LOCALMOCK_COMPONENT_TEST=1 for real image tests")
	}
	h := setup(t)
	selected := components.Templates()
	delete(selected, "nacos")
	doc := loader.Document{Scenario: scenario.Scenario{Name: "only-datastores", Components: selected}, Services: map[string]catalog.Service{}}
	file, err := loader.Export(doc, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := h.start(file)
	h.verifyDatastores(r.ID, "redis")
	if _, err = h.command("probe", "--deployment", "mysql", "--interface", "sql", r.ID); err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command("docker", "inspect", "--format", "{{json .Mounts}}", h.container(r.ID, "mysql")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var mounts []struct{ Type, Name string }
	if err = json.Unmarshal(b, &mounts); err != nil {
		t.Fatal(err)
	}
	if _, err = h.command("down", r.ID); err != nil {
		t.Fatal(err)
	}
	for _, m := range mounts {
		if m.Type == "volume" {
			if err := exec.Command("docker", "volume", "inspect", m.Name).Run(); err == nil {
				t.Fatalf("anonymous data volume retained: %s", m.Name)
			}
		}
	}
	h.assertRemoved(r.ID)
}

func TestSandboxDeniesHostOtherRunAndExternalDNS(t *testing.T) {
	h := setup(t)
	file := filepath.Join(h.root, "examples/http-smoke/scenario.yaml")
	a := h.start(file)
	b := h.start(file)
	addresses := h.addresses(a.ID)
	other := h.addresses(b.ID)
	if addresses["caller"] == other["caller"] {
		t.Fatal("runs share allocated subnet")
	}
	if got := h.rawProbe(a.ID, execution.ProbeCall{Protocol: "tcp", Address: net.JoinHostPort(addresses["caller"], "8080")}); got.Code != "OK" {
		t.Fatalf("positive control failed: %+v", got)
	}
	if got := h.rawProbe(a.ID, execution.ProbeCall{Protocol: "tcp", Address: net.JoinHostPort(other["caller"], "8080")}); got.Error == "" {
		t.Fatal("other Run reachable")
	}
	if got := h.rawProbe(a.ID, execution.ProbeCall{Protocol: "tcp", Address: "not-selected.localmock.invalid:80"}); got.Error == "" {
		t.Fatal("external DNS resolved/reachable")
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var reached atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			reached.Add(1)
			conn.Close()
		}
	}()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, address := range addrs {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil {
				continue
			}
			checked++
			if got := h.rawProbe(a.ID, execution.ProbeCall{Protocol: "tcp", Address: net.JoinHostPort(ip.String(), port)}); got.Error == "" {
				t.Fatalf("host address reachable: %s", ip)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no host IPv4 address to verify")
	}
	if reached.Load() != 0 {
		t.Fatal("sandbox traffic reached host canary")
	}
	// Independently prove that the canary is listening.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}
