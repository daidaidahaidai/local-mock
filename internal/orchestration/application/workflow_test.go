package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/daidaidahaidai/local-mock/internal/orchestration/domain"
)

type workflowProject struct {
	saved map[string]any
	order []string
}

func (p *workflowProject) Context(string) any                       { return map[string]string{"catalog": "fixture"} }
func (p *workflowProject) RepositoryTools() []Tool                  { return nil }
func (p *workflowProject) ValidateRepository(json.RawMessage) error { return nil }
func (p *workflowProject) CheckTopology(domain.Topology) error      { return nil }
func (p *workflowProject) ApplyTopology(domain.Topology)            { p.order = append(p.order, "topology") }
func (p *workflowProject) CheckConfig(domain.Config) error          { return nil }
func (p *workflowProject) ApplyConfig(domain.Config)                { p.order = append(p.order, "config") }
func (p *workflowProject) CheckMocks(domain.Mocks) error            { return nil }
func (p *workflowProject) ApplyMocks(domain.Mocks)                  { p.order = append(p.order, "mock") }
func (p *workflowProject) CheckRuntime(domain.Runtime) error        { return nil }
func (p *workflowProject) Export(context.Context) (string, error) {
	p.order = append(p.order, "export")
	return "scenario.yaml", nil
}
func (p *workflowProject) Save(n string, v any) error { p.saved[n] = v; return nil }

type workflowExecutor struct {
	calls, cleanups int
	failure         error
	failedProbe     bool
	cancel          context.CancelFunc
}

func (e *workflowExecutor) Execute(context.Context, string, domain.Runtime) (domain.Evidence, error) {
	e.calls++
	if e.cancel != nil {
		e.cancel()
	}
	pass := !e.failedProbe
	return domain.Evidence{Executed: true, RunID: "run", Observations: []domain.Observation{{ID: "probe", Kind: "probe", Detail: "fixture", Passed: &pass}}}, e.failure
}
func (e *workflowExecutor) Cleanup() error { e.cleanups++; return nil }

type sequenceModel struct {
	calls        int
	debugFailure bool
	onCall       func([]Message, []ToolSpec)
}

func (m *sequenceModel) Complete(_ context.Context, msgs []Message, tools []ToolSpec) (Completion, error) {
	m.calls++
	if m.onCall != nil {
		m.onCall(msgs, tools)
	}
	if m.calls == 6 && m.debugFailure {
		return Completion{}, errors.New("provider unavailable")
	}
	content := "{}"
	if m.calls == 6 {
		var input struct {
			Evidence domain.Evidence `json:"evidence"`
		}
		var user struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(msgs[1], &user)
		_ = json.Unmarshal([]byte(user.Content), &input)
		content = fmt.Sprintf(`{"verdict":%q,"findings":[{"summary":"observed","evidence":[%q]}],"next_steps":[]}`, input.Evidence.Verdict(), input.Evidence.Observations[0].ID)
	}
	return Completion{Message: message(map[string]string{"role": "assistant", "content": content}), Content: content, Usage: Usage{TotalTokens: 10}}, nil
}
func TestSixIndependentAgentsAndExecutionOwnership(t *testing.T) {
	for _, tc := range []struct {
		name                                           string
		execute, cleanup, failed, debugFailure, cancel bool
		failure                                        error
		wantCalls, wantClean                           int
		wantErr                                        bool
	}{
		{name: "plan", wantCalls: 6},
		{name: "keep", execute: true, wantCalls: 6},
		{name: "cleanup", execute: true, cleanup: true, wantCalls: 6, wantClean: 1},
		{name: "probe-fails", execute: true, failed: true, wantCalls: 6, wantClean: 1, wantErr: true},
		{name: "startup-fails", execute: true, failure: errors.New("startup"), wantCalls: 6, wantClean: 1, wantErr: true},
		{name: "debug-provider-fails", execute: true, debugFailure: true, wantCalls: 6, wantClean: 1, wantErr: true},
		{name: "cancel-after-start", execute: true, cancel: true, wantCalls: 5, wantClean: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &workflowProject{saved: map[string]any{}}
			e := &workflowExecutor{failedProbe: tc.failed, failure: tc.failure}
			if tc.cancel {
				e.cancel = cancel
			}
			roles := []string{"Repository", "Topology", "Config", "Mock", "Runtime", "Debug"}
			m := &sequenceModel{debugFailure: tc.debugFailure}
			m.onCall = func(messages []Message, tools []ToolSpec) {
				if len(messages) != 2 {
					t.Fatalf("history leaked across agents: %d", len(messages))
				}
				if !strings.Contains(string(messages[0]), "role is "+roles[m.calls-1]+" Agent") {
					t.Fatalf("wrong independent role: %s", messages[0])
				}
				if len(tools) != 0 {
					t.Fatal("unexpected tools")
				}
			}
			result, err := (Workflow{Runner: Runner{Model: m}, Project: p, Executor: e}).Run(ctx, WorkflowRequest{Task: "fixture", MaxRounds: 2, MaxTokens: 100, Execute: tc.execute, Cleanup: tc.cleanup})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			if m.calls != tc.wantCalls || e.cleanups != tc.wantClean {
				t.Fatalf("calls=%d cleanups=%d", m.calls, e.cleanups)
			}
			if tc.execute && e.calls != 1 {
				t.Fatalf("runtime effects replayed: %d", e.calls)
			}
			if !tc.execute && e.calls != 0 {
				t.Fatal("plan started runtime")
			}
			if _, ok := p.saved["workflow.json"]; !ok {
				t.Fatal("missing final journal")
			}
			if result.Status == "completed" && tc.wantErr {
				t.Fatal("failure recorded completed")
			}
			if strings.Join(p.order, ",") != "topology,config,mock,export" {
				t.Fatalf("wrong handoff order: %v", p.order)
			}
		})
	}
}
func TestWorkflowStopsBeforeNextRequestAtTokenBudget(t *testing.T) {
	p := &workflowProject{saved: map[string]any{}}
	m := &sequenceModel{}
	result, err := (Workflow{Runner: Runner{Model: m}, Project: p}).Run(context.Background(), WorkflowRequest{Task: "fixture", MaxRounds: 2, MaxTokens: 10})
	if err == nil || m.calls != 1 || result.Stage != "topology" {
		t.Fatalf("result=%+v calls=%d error=%v", result, m.calls, err)
	}
}

type blockedModel struct{ calls int }

func (m *blockedModel) Complete(context.Context, []Message, []ToolSpec) (Completion, error) {
	m.calls++
	content := `{"blocked":"requested protocol is unsupported"}`
	return Completion{Message: message(map[string]string{"role": "assistant", "content": content}), Content: content}, nil
}
func TestBlockedTaskDoesNotRetryOrExecute(t *testing.T) {
	p := &workflowProject{saved: map[string]any{}}
	m := &blockedModel{}
	e := &workflowExecutor{}
	result, err := (Workflow{Runner: Runner{Model: m}, Project: p, Executor: e}).Run(context.Background(), WorkflowRequest{Task: "unsupported request", MaxRounds: 5, MaxTokens: 100, Execute: true})
	if err == nil || m.calls != 1 || e.calls != 0 || result.Status != "failed" {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, m.calls)
	}
	if !strings.Contains(string(result.Stages[0].Content), "unsupported") {
		t.Fatal("blocked reason not retained")
	}
}

type failedJournalProject struct {
	workflowProject
	calls int
}

func (p *failedJournalProject) Save(name string, value any) error {
	if name == "workflow.json" {
		p.calls++
		if p.calls == 7 {
			return errors.New("disk full")
		}
	}
	return p.workflowProject.Save(name, value)
}
func TestFinalJournalFailureAlsoCleansSuccessfulRun(t *testing.T) {
	p := &failedJournalProject{workflowProject: workflowProject{saved: map[string]any{}}}
	e := &workflowExecutor{}
	m := &sequenceModel{}
	result, err := (Workflow{Runner: Runner{Model: m}, Project: p, Executor: e}).Run(context.Background(), WorkflowRequest{Task: "fixture", MaxRounds: 1, MaxTokens: 100, Execute: true})
	if err == nil || e.cleanups != 1 || !result.CleanedUp || result.Status != "failed" {
		t.Fatalf("result=%+v err=%v cleanups=%d", result, err, e.cleanups)
	}
}

type fabricatedDebugModel struct{ sequenceModel }

func (m *fabricatedDebugModel) Complete(ctx context.Context, msgs []Message, tools []ToolSpec) (Completion, error) {
	if m.calls >= 5 {
		m.calls++
		content := `{"verdict":"passed","findings":[{"summary":"made up","evidence":["nonexistent"]}],"next_steps":[]}`
		return Completion{Message: message(map[string]string{"role": "assistant", "content": content}), Content: content}, nil
	}
	return m.sequenceModel.Complete(ctx, msgs, tools)
}
func TestDebugCannotClaimSuccessWithoutEvidence(t *testing.T) {
	p := &workflowProject{saved: map[string]any{}}
	m := &fabricatedDebugModel{}
	e := &workflowExecutor{}
	result, err := (Workflow{Runner: Runner{Model: m}, Project: p, Executor: e}).Run(context.Background(), WorkflowRequest{Task: "fixture", MaxRounds: 2, MaxTokens: 100, Execute: true})
	if err == nil || result.Status != "failed" || e.cleanups != 1 || m.calls != 7 {
		t.Fatalf("result=%+v err=%v cleanups=%d calls=%d", result, err, e.cleanups, m.calls)
	}
}

func TestBudgetExhaustedByRuntimeArtifactDoesNotStartContainers(t *testing.T) {
	p := &workflowProject{saved: map[string]any{}}
	m := &sequenceModel{}
	e := &workflowExecutor{}
	_, err := (Workflow{Runner: Runner{Model: m}, Project: p, Executor: e}).Run(context.Background(), WorkflowRequest{Task: "fixture", MaxRounds: 1, MaxTokens: 50, Execute: true})
	if err == nil || m.calls != 5 || e.calls != 0 {
		t.Fatalf("error=%v model calls=%d runtime calls=%d", err, m.calls, e.calls)
	}
}
