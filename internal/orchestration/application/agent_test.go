package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type fakeModel struct{ calls int }

func (m *fakeModel) Complete(_ context.Context, messages []Message, _ []ToolSpec) (Completion, error) {
	m.calls++
	if m.calls == 1 {
		var call ToolCall
		call.ID = "one"
		call.Type = "function"
		call.Function.Name = "not_allowed"
		call.Function.Arguments = "{}"
		return Completion{Message: json.RawMessage(`{"role":"assistant","content":null,"reasoning_content":"continuation"}`), Calls: []ToolCall{call}}, nil
	}
	b, _ := json.Marshal(messages)
	if !strings.Contains(string(b), "tool not permitted") || !strings.Contains(string(b), "reasoning_content") {
		return Completion{}, fmt.Errorf("lost tool history")
	}
	return Completion{Message: json.RawMessage(`{"role":"assistant","content":"{}"}`), Content: "{}"}, nil
}
func TestUnknownToolIsRejectedAndHistoryPreserved(t *testing.T) {
	m := &fakeModel{}
	result, err := (Runner{Model: m}).Run(context.Background(), Definition{Name: "test", MaxRounds: 2}, "inspect", nil, nil)
	if err != nil || result.Rounds != 2 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestInvalidArtifactStopsAtBudget(t *testing.T) {
	m := &fakeModel{}
	_, err := (Runner{Model: m}).Run(context.Background(), Definition{Name: "test", MaxRounds: 2}, "inspect", nil, func(json.RawMessage) error { return fmt.Errorf("missing evidence") })
	if err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatal(err)
	}
}
