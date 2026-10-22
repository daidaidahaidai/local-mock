package domain

type Spec struct {
	Name      string              `json:"name"`
	Inputs    map[string]string   `json:"inputs"`
	Workloads map[string]Workload `json:"workloads"`
	Network   string              `json:"network"` // isolated; no external connectivity
}
type Workload struct {
	Service    string              `json:"service"`
	Role       string              `json:"role"`
	Image      string              `json:"image,omitempty"`
	Build      *BuildSpec          `json:"build,omitempty"`
	Command    []string            `json:"command,omitempty"`
	Env        map[string]string   `json:"env"`
	Publish    []PublishedPort     `json:"publish,omitempty"`
	Endpoints  map[string]Endpoint `json:"endpoints,omitempty"`
	IPOffset   int                 `json:"ip_offset"`
	MemoryMB   int                 `json:"memory_mb,omitempty"`
	Readiness  []string            `json:"readiness"`
	MockBundle []byte              `json:"mock_bundle,omitempty"`
}
type Endpoint struct {
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Descriptors []byte `json:"descriptors,omitempty"`
	Service     string `json:"service,omitempty"`
}
type BuildSpec struct {
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile"`
	Args       map[string]string `json:"args,omitempty"`
}
type PublishedPort struct {
	Target    int `json:"target"`
	Published int `json:"published"`
}
