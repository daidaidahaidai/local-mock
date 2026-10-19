package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	agent "github.com/daidaidahaidai/local-mock/internal/orchestration/application"
)

type Client struct {
	HTTP      *http.Client
	BaseURL   string
	APIKey    string
	Model     string
	MaxTokens int
}

func New(key, model string) *Client {
	if model == "" {
		model = "deepseek-flash"
	}
	return &Client{HTTP: &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, BaseURL: "https://api.deepseek.com", APIKey: key, Model: model, MaxTokens: 4096}
}
func (c *Client) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Completion, error) {
	if c.APIKey == "" {
		return agent.Completion{}, fmt.Errorf("DEEPSEEK_API_KEY is not set in this process")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" {
		return agent.Completion{}, fmt.Errorf("invalid model endpoint")
	}
	if u.Scheme != "https" && u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" {
		return agent.Completion{}, fmt.Errorf("model endpoint requires HTTPS")
	}
	body, err := json.Marshal(map[string]any{"model": c.Model, "messages": messages, "tools": tools, "stream": false, "max_tokens": c.MaxTokens, "thinking": map[string]string{"type": "disabled"}})
	if err != nil {
		return agent.Completion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return agent.Completion{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return agent.Completion{}, fmt.Errorf("model request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return agent.Completion{}, fmt.Errorf("DeepSeek returned HTTP %d (request body and credentials omitted)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil {
		return agent.Completion{}, err
	}
	if len(data) > 8<<20 {
		return agent.Completion{}, fmt.Errorf("model response exceeds 8 MiB")
	}
	var envelope struct {
		Choices []struct {
			Message      json.RawMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		} `json:"choices"`
		Usage agent.Usage `json:"usage"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return agent.Completion{}, fmt.Errorf("invalid model response: %w", err)
	}
	if len(envelope.Choices) != 1 {
		return agent.Completion{}, fmt.Errorf("expected one model choice")
	}
	choice := envelope.Choices[0]
	var msg struct {
		Role    string           `json:"role"`
		Content string           `json:"content"`
		Calls   []agent.ToolCall `json:"tool_calls"`
	}
	if err = json.Unmarshal(choice.Message, &msg); err != nil {
		return agent.Completion{}, err
	}
	if msg.Role != "assistant" {
		return agent.Completion{}, fmt.Errorf("unexpected response role")
	}
	return agent.Completion{Message: choice.Message, Content: msg.Content, Calls: msg.Calls, FinishReason: choice.FinishReason, Usage: envelope.Usage}, nil
}
