package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	execapp "github.com/daidaidahaidai/local-mock/internal/execution/application"
	"github.com/daidaidahaidai/local-mock/internal/execution/infrastructure/docker"
	"github.com/daidaidahaidai/local-mock/internal/execution/infrastructure/lock"
	"github.com/daidaidahaidai/local-mock/internal/execution/infrastructure/sqlite"
	agent "github.com/daidaidahaidai/local-mock/internal/orchestration/application"
	"github.com/daidaidahaidai/local-mock/internal/orchestration/domain"
	workflow "github.com/daidaidahaidai/local-mock/internal/orchestration/infrastructure/workflow"
)

// The model is scripted, but every artifact/tool/validator, Docker operation,
// request, observation and cleanup is real. Ordinary CI never spends API tokens.
type workflowModel struct {
	failProbe bool
	roles     map[string]bool
}

func (m *workflowModel) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Completion, error) {
	var system struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(messages[0], &system)
	content := ""
	role := ""
	for _, name := range []string{"Repository", "Topology", "Config", "Mock", "Runtime", "Debug"} {
		if strings.Contains(system.Content, "role is "+name+" Agent") {
			role = name
		}
	}
	m.roles[role] = true
	switch role {
	case "Repository":
		if len(messages) == 2 {
			if len(tools) != 2 {
				return agent.Completion{}, fmt.Errorf("repository tool permissions missing")
			}
			var call agent.ToolCall
			call.ID = "inspect-service"
			call.Type = "function"
			call.Function.Name = "read_file"
			call.Function.Arguments = `{"path":"service.yaml"}`
			raw, _ := json.Marshal(map[string]any{"role": "assistant", "content": "", "tool_calls": []agent.ToolCall{call}})
			return agent.Completion{Message: raw, Calls: []agent.ToolCall{call}}, nil
		}
		content = `{"services":[{"name":"echo","language":"Go","build_files":[],"contract_files":[],"config_files":["service.yaml"],"evidence":["service.yaml"]}],"unknowns":[]}`
	case "Topology":
		content = `{"name":"agent-http","deployments":{"caller":{"service":"echo","implementation":"real","publish":{"api":0}},"dependency":{"service":"echo","implementation":"mock"}},"bindings":{"caller":{"backend":"dependency"}},"reason":"whole dependency mock"}`
	case "Config":
		content = `{"env":{"dependency":{"MOCK_RESPONSE":"from-agent-workflow"}},"reason":"test marker"}`
	case "Mock":
		content = `{"mocks":[],"reason":"existing code mock is configured with environment"}`
	case "Runtime":
		expected := "from-agent-workflow"
		if m.failProbe {
			expected = "deliberately-missing"
		}
		content = fmt.Sprintf(`{"probes":[{"name":"request","deployment":"caller","interface":"api","path":"/","expect_code":"200","contains":%q}],"reason":"verify real forwarder calls mock"}`, expected)
	case "Debug":
		var user struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(messages[1], &user)
		var input struct {
			Evidence domain.Evidence `json:"evidence"`
		}
		_ = json.Unmarshal([]byte(user.Content), &input)
		content = fmt.Sprintf(`{"verdict":%q,"findings":[{"summary":"request assertion evaluated","evidence":["probe-request"]}],"next_steps":[]}`, input.Evidence.Verdict())
	default:
		return agent.Completion{}, fmt.Errorf("unexpected role")
	}
	raw, _ := json.Marshal(map[string]string{"role": "assistant", "content": content})
	return agent.Completion{Message: raw, Content: content, Usage: agent.Usage{TotalTokens: 1}}, nil
}
func TestSixAgentWorkflowRealDocker(t *testing.T) {
	h := setup(t)
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint("failed-assertion-", failure), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			repo := filepath.Join(h.root, "examples/http-smoke")
			project, err := workflow.New(ctx, repo, filepath.Join(repo, "scenario.yaml"), filepath.Join(t.TempDir(), "artifacts"))
			if err != nil {
				t.Fatal(err)
			}
			store, err := sqlite.Open(h.state)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			manager := execapp.Manager{Root: h.state, Store: store, Driver: docker.Driver{Progress: io.Discard, WaitSeconds: 90}, Locker: lock.Locker{Root: h.state}}
			executor := &workflow.Executor{Manager: manager}
			model := &workflowModel{failProbe: failure, roles: map[string]bool{}}
			result, err := (agent.Workflow{Runner: agent.Runner{Model: model}, Project: project, Executor: executor}).Run(ctx, agent.WorkflowRequest{Task: "forward a request to configured mock", MaxRounds: 3, MaxTokens: 100, Execute: true, Cleanup: true})
			if (err != nil) != failure {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if len(model.roles) != 6 || len(result.Stages) != 6 {
				t.Fatalf("missing agent stages: %v", model.roles)
			}
			if result.Evidence.RunID == "" {
				t.Fatal("no execution run")
			}
			h.assertRemoved(result.Evidence.RunID)
			record, err := store.Get(ctx, result.Evidence.RunID)
			if err != nil || record.State != "removed" {
				t.Fatalf("cleanup not recorded: %+v %v", record, err)
			}
			if result.Evidence.Verdict() == "passed" && failure {
				t.Fatal("failed assertion presented as success")
			}
		})
	}
}
