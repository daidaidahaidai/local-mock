package domain

import "encoding/json"

type Probe struct {
	Deployment string          `json:"deployment"`
	Interface  string          `json:"interface"`
	Path       string          `json:"path,omitempty"`
	Method     string          `json:"method,omitempty"`
	Request    json.RawMessage `json:"request,omitempty"`
}

// ProbeCall is sent over Docker stdin to the sandbox runner, never over a host socket.
type ProbeCall struct {
	Protocol    string          `json:"protocol"`
	Address     string          `json:"address"`
	Descriptors []byte          `json:"descriptors,omitempty"`
	Path        string          `json:"path,omitempty"`
	Method      string          `json:"method,omitempty"`
	Request     json.RawMessage `json:"request,omitempty"`
}
type ProbeResult struct {
	Code     string          `json:"code"`
	TraceID  string          `json:"trace_id,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
	Body     string          `json:"body,omitempty"`
	Error    string          `json:"error,omitempty"`
}
