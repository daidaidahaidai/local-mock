package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	execapp "github.com/daidaidahaidai/local-mock/internal/execution/application"
	execution "github.com/daidaidahaidai/local-mock/internal/execution/domain"
	"github.com/daidaidahaidai/local-mock/internal/mocking/infrastructure/grpcmock"
	"github.com/daidaidahaidai/local-mock/internal/orchestration/domain"
	scenarioapp "github.com/daidaidahaidai/local-mock/internal/scenario/application"
	loader "github.com/daidaidahaidai/local-mock/internal/scenario/infrastructure/yaml"
)

type Executor struct {
	Manager execapp.Manager
	Run     execution.Run
}

func (e *Executor) Execute(ctx context.Context, file string, r domain.Runtime) (domain.Evidence, error) {
	evidence := domain.Evidence{Executed: true}
	doc, err := loader.LoadContext(ctx, file)
	if err != nil {
		return evidence, err
	}
	spec, err := scenarioapp.Resolve(doc.Scenario, doc.Services, doc.Files)
	if err != nil {
		return evidence, err
	}
	e.Run, err = e.Manager.Start(ctx, doc.Scenario.Name, spec)
	evidence.RunID = e.Run.ID
	if err != nil {
		passed := false
		evidence.Observations = append(evidence.Observations, domain.Observation{ID: "startup", Kind: "startup", Detail: err.Error(), Passed: &passed})
		return evidence, err
	}
	status, err := e.Manager.Driver.Status(ctx, e.Run)
	if err != nil {
		return evidence, err
	}
	evidence.Observations = append(evidence.Observations, domain.Observation{ID: "containers", Kind: "containers", Detail: string(status)})
	var containers []struct {
		Deployment string                                         `json:"deployment"`
		Ports      map[string][]struct{ HostIP, HostPort string } `json:"ports"`
	}
	if err = json.Unmarshal(status, &containers); err != nil {
		return evidence, err
	}
	for _, probe := range r.Probes {
		if err = ctx.Err(); err != nil {
			return evidence, err
		}
		d := doc.Scenario.Deployments[probe.Deployment]
		svc := doc.Services[d.Service]
		contract := svc.Provides[probe.Interface]
		port := svc.Implementations[d.Implementation].Endpoints[probe.Interface].Port
		address := ""
		for _, c := range containers {
			if c.Deployment != probe.Deployment {
				continue
			}
			for _, binding := range c.Ports[strconv.Itoa(port)+"/tcp"] {
				if binding.HostIP == "127.0.0.1" {
					n, parseErr := strconv.Atoi(binding.HostPort)
					if parseErr == nil && n > 0 && n < 65536 {
						address = net.JoinHostPort("127.0.0.1", binding.HostPort)
					}
				}
			}
		}
		code, detail := "", "no loopback port found for selected deployment"
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if address != "" {
			if contract.Protocol == "http" {
				code, detail = probeHTTP(callCtx, address, probe.Path)
			} else {
				result, callErr := grpcmock.Invoke(callCtx, address, contract.Contract, probe.Method, probe.Request)
				code = result.Code
				b, _ := json.Marshal(result)
				detail = string(b)
				if callErr != nil {
					detail += "\n" + callErr.Error()
				}
			}
		}
		cancel()
		expected := probe.ExpectCode
		if contract.Protocol == "grpc" {
			expected = canonicalGRPCCode(expected)
		}
		passed := code == expected && (probe.Contains == "" || strings.Contains(detail, probe.Contains))
		evidence.Observations = append(evidence.Observations, domain.Observation{ID: "probe-" + probe.Name, Kind: "probe", Detail: fmt.Sprintf("expected=%s observed=%s contains=%q\n%s", probe.ExpectCode, code, probe.Contains, detail), Passed: &passed})
	}
	logs := &limitedLog{Limit: 16384}
	if logErr := e.Manager.Driver.Logs(ctx, e.Run, logs); logErr != nil {
		evidence.Observations = append(evidence.Observations, domain.Observation{ID: "logs-error", Kind: "gap", Detail: logErr.Error()})
	}
	evidence.Observations = append(evidence.Observations, domain.Observation{ID: "logs", Kind: "logs", Detail: logs.String()})
	return evidence, ctx.Err()
}
func (e *Executor) Cleanup() error {
	if e.Run.ID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, err := e.Manager.Down(ctx, e.Run.ID)
	return err
}
func probeHTTP(ctx context.Context, address, path string) (string, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
	if err != nil {
		return "", err.Error()
	}
	var trace [16]byte
	var span [8]byte
	if _, err = rand.Read(trace[:]); err != nil {
		return "", err.Error()
	}
	if _, err = rand.Read(span[:]); err != nil {
		return "", err.Error()
	}
	traceID := hex.EncodeToString(trace[:])
	req.Header.Set("traceparent", "00-"+traceID+"-"+hex.EncodeToString(span[:])+"-01")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", err.Error()
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32769))
	if err != nil {
		return "", err.Error()
	}
	if len(b) > 32768 {
		return "", "response exceeds 32 KiB"
	}
	return strconv.Itoa(resp.StatusCode), "trace_id=" + traceID + "\n" + string(b)
}

// Continue consuming output after the cap so verbose containers cannot break log capture.
type limitedLog struct {
	Limit     int
	b         strings.Builder
	truncated bool
}

func (l *limitedLog) Write(p []byte) (int, error) {
	n := len(p)
	remaining := l.Limit - l.b.Len()
	if len(p) > remaining {
		p = p[:remaining]
		l.truncated = true
	}
	l.b.Write(p)
	return n, nil
}
func (l *limitedLog) String() string {
	if l.truncated {
		return l.b.String() + "\n[truncated]"
	}
	return l.b.String()
}
