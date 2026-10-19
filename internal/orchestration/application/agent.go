package application

import (
	"context"
	"encoding/json"
	"fmt"
)

type Message = json.RawMessage
type ToolSpec struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}
type FunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
type Completion struct {
	Message      Message
	Content      string
	Calls        []ToolCall
	FinishReason string
	Usage        Usage
}
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
type Model interface {
	Complete(context.Context, []Message, []ToolSpec) (Completion, error)
}
type Tool interface {
	Spec() ToolSpec
	Call(context.Context, json.RawMessage) (any, error)
}
type Definition struct {
	Name         string
	Instructions string
	MaxRounds    int
	AllowedTools []string
}
type Result struct {
	Agent   string          `json:"agent"`
	Content json.RawMessage `json:"content"`
	Rounds  int             `json:"rounds"`
	Usage   Usage           `json:"usage"`
}
type Event struct {
	Agent string
	Round int
	Tool  string
}
type Runner struct {
	Model   Model
	OnEvent func(Event)
}

func (r Runner) Run(ctx context.Context, definition Definition, input any, tools []Tool, validate func(json.RawMessage) error) (Result, error) {
	if definition.MaxRounds < 1 || definition.MaxRounds > 100 {
		return Result{}, fmt.Errorf("max rounds must be 1..100")
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return Result{}, err
	}
	messages := []Message{message(map[string]any{"role": "system", "content": definition.Instructions}), message(map[string]any{"role": "user", "content": string(inputJSON)})}
	byName := map[string]Tool{}
	specs := []ToolSpec{}
	allowed := map[string]bool{}
	for _, name := range definition.AllowedTools {
		allowed[name] = true
	}
	for _, tool := range tools {
		spec := tool.Spec()
		name := spec.Function.Name
		if !allowed[name] {
			return Result{}, fmt.Errorf("tool %s is not allowed for %s", name, definition.Name)
		}
		if _, ok := byName[name]; ok {
			return Result{}, fmt.Errorf("duplicate tool %s", name)
		}
		byName[name] = tool
		specs = append(specs, spec)
	}
	result := Result{Agent: definition.Name}
	for round := 1; round <= definition.MaxRounds; round++ {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if r.OnEvent != nil {
			r.OnEvent(Event{Agent: definition.Name, Round: round})
		}
		response, err := r.Model.Complete(ctx, messages, specs)
		if err != nil {
			return result, err
		}
		result.Rounds = round
		result.Usage.PromptTokens += response.Usage.PromptTokens
		result.Usage.CompletionTokens += response.Usage.CompletionTokens
		result.Usage.TotalTokens += response.Usage.TotalTokens
		// Preserve the complete provider message, including reasoning_content for tool turns.
		messages = append(messages, response.Message)
		if response.FinishReason == "length" {
			return result, fmt.Errorf("model output truncated; increase output budget")
		}
		if len(response.Calls) == 0 {
			content := json.RawMessage(response.Content)
			if !json.Valid(content) {
				messages = append(messages, message(map[string]any{"role": "user", "content": "Return only one valid JSON object without Markdown fences."}))
				continue
			}
			if validate != nil {
				if err = validate(content); err != nil {
					messages = append(messages, message(map[string]any{"role": "user", "content": "Artifact validation failed: " + err.Error() + ". Correct the artifact using observed evidence."}))
					continue
				}
			}
			result.Content = content
			return result, nil
		}
		if len(response.Calls) > 16 {
			return result, fmt.Errorf("too many tools in one response")
		}
		seen := map[string]bool{}
		for _, call := range response.Calls {
			if call.ID == "" || seen[call.ID] {
				return result, fmt.Errorf("missing or duplicate tool call ID")
			}
			seen[call.ID] = true
			if err = ctx.Err(); err != nil {
				return result, err
			}
			if r.OnEvent != nil {
				r.OnEvent(Event{Agent: definition.Name, Round: round, Tool: call.Function.Name})
			}
			var output any
			tool, ok := byName[call.Function.Name]
			if !ok {
				output = map[string]string{"error": "tool not permitted"}
			} else if !json.Valid([]byte(call.Function.Arguments)) {
				output = map[string]string{"error": "invalid JSON arguments"}
			} else {
				value, toolErr := tool.Call(ctx, json.RawMessage(call.Function.Arguments))
				if toolErr != nil {
					output = map[string]string{"error": toolErr.Error()}
				} else {
					output = value
				}
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				return result, err
			}
			if len(encoded) > 65536 {
				encoded = []byte(`{"error":"tool result too large; request a smaller scope"}`)
			}
			messages = append(messages, message(map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(encoded)}))
		}
	}
	return result, fmt.Errorf("agent %s exhausted %d rounds", definition.Name, definition.MaxRounds)
}
func message(v any) Message { b, _ := json.Marshal(v); return b }
