package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	execapp "github.com/daidaidahaidai/local-mock/internal/execution/application"
	execution "github.com/daidaidahaidai/local-mock/internal/execution/domain"
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
	prober, ok := e.Manager.Driver.(execapp.Prober)
	if !ok {
		return evidence, fmt.Errorf("runtime does not support network-local probes")
	}
	for _, probe := range r.Probes {
		if err = ctx.Err(); err != nil {
			return evidence, err
		}
		d := doc.Scenario.Deployments[probe.Deployment]
		svc := doc.Services[d.Service]
		contract := svc.Provides[probe.Interface]
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		result, callErr := prober.Probe(callCtx, e.Run, execution.Probe{Deployment: probe.Deployment, Interface: probe.Interface, Path: probe.Path, Method: probe.Method, Request: probe.Request})
		code := result.Code
		b, _ := json.Marshal(result)
		detail := string(b)
		if callErr != nil {
			code = ""
			detail = callErr.Error()
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
