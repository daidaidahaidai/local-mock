package domain

import "encoding/json"

// Artifacts are the explicit hand-off contract between independently scoped agents.
type Topology struct {
	Name        string                       `json:"name"`
	Deployments map[string]Deployment        `json:"deployments"`
	Bindings    map[string]map[string]string `json:"bindings"`
	Reason      string                       `json:"reason"`
}
type Deployment struct {
	Service        string         `json:"service"`
	Implementation string         `json:"implementation"`
	Publish        map[string]int `json:"publish,omitempty"`
}
type Config struct {
	Env    map[string]map[string]string `json:"env"`
	Reason string                       `json:"reason"`
}
type Mock struct {
	Service        string `json:"service"`
	Implementation string `json:"implementation"`
	Rules          []Rule `json:"rules"`
}
type Rule struct {
	Method       string          `json:"method"`
	Response     json.RawMessage `json:"response,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	DelayMS      int             `json:"delay_ms,omitempty"`
}
type Mocks struct {
	Mocks  []Mock `json:"mocks"`
	Reason string `json:"reason"`
}
type Runtime struct {
	Probes []Probe `json:"probes"`
	Reason string  `json:"reason"`
}
type Probe struct {
	Name       string `json:"name"`
	Deployment string `json:"deployment"`
	Interface  string `json:"interface"`
	// HTTP probes are GET only; gRPC probes name a unary method in the declared service.
	Path       string          `json:"path,omitempty"`
	Method     string          `json:"method,omitempty"`
	Request    json.RawMessage `json:"request,omitempty"`
	ExpectCode string          `json:"expect_code"`
	Contains   string          `json:"contains,omitempty"`
}
type Observation struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Passed *bool  `json:"passed,omitempty"`
}
type Evidence struct {
	Executed     bool          `json:"executed"`
	RunID        string        `json:"run_id,omitempty"`
	Observations []Observation `json:"observations"`
}

func (e Evidence) Verdict() string {
	if !e.Executed {
		return "not_executed"
	}
	for _, o := range e.Observations {
		if o.Passed != nil && !*o.Passed {
			return "failed"
		}
	}
	return "passed"
}

type Finding struct {
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
}
type Debug struct {
	Verdict   string    `json:"verdict"`
	Findings  []Finding `json:"findings"`
	NextSteps []string  `json:"next_steps"`
}
