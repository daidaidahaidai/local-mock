package domain

type Spec struct {
	Name      string              `json:"name"`
	Inputs    map[string]string   `json:"inputs"`
	Workloads map[string]Workload `json:"workloads"`
}
type Workload struct {
	Service    string            `json:"service"`
	Role       string            `json:"role"`
	Image      string            `json:"image,omitempty"`
	Build      *BuildSpec        `json:"build,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Env        map[string]string `json:"env"`
	Publish    []PublishedPort   `json:"publish,omitempty"`
	Readiness  []string          `json:"readiness"`
	MockBundle []byte            `json:"mock_bundle,omitempty"`
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
