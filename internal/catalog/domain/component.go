package domain

// Component is a user-selected infrastructure image, not an agent decision.
// Roles describe its uses; one Nacos instance can serve registry and config.
type Component struct {
	Roles     []string          `json:"roles"`
	Image     string            `json:"image"`
	Command   []string          `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Endpoints map[string]int    `json:"endpoints"`
	Readiness []string          `json:"readiness"`
	MemoryMB  int               `json:"memory_mb,omitempty"`
	IPOffset  int               `json:"ip_offset"`
}
