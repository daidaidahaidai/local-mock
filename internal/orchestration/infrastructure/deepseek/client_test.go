package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agent "github.com/daidaidahaidai/local-mock/internal/orchestration/application"
)

func TestPreservesToolMessageAndDoesNotSerializeCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-key" {
			t.Error("missing auth")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(body)
		if strings.Contains(string(raw), "fake-key") {
			t.Error("credential in body")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"reasoning_content":"provider-continuation","tool_calls":[{"id":"1","type":"function","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":9}}`))
	}))
	defer server.Close()
	c := New("fake-key", "test")
	c.BaseURL = server.URL
	got, err := c.Complete(context.Background(), []agent.Message{json.RawMessage(`{"role":"user","content":"inspect"}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Calls) != 1 || !strings.Contains(string(got.Message), "reasoning_content") || got.Usage.TotalTokens != 9 {
		t.Fatalf("%+v", got)
	}
}
func TestProviderErrorDoesNotReplayOrLeakBody(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(429)
		_, _ = w.Write([]byte("fake-key"))
	}))
	defer server.Close()
	c := New("fake-key", "test")
	c.BaseURL = server.URL
	_, err := c.Complete(context.Background(), nil, nil)
	if err == nil || strings.Contains(err.Error(), "fake-key") || calls != 1 {
		t.Fatalf("%v calls=%d", err, calls)
	}
}
