package domain

// Component is a user-selected infrastructure image, not an agent decision.
// Roles describe its uses; one Nacos instance can serve registry and config.
type Component struct {
	Roles      []string          `json:"roles"`
	Image      string            `json:"image"`
	Command    []string          `json:"command,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Endpoints  map[string]int    `json:"endpoints"`
	Readiness  []string          `json:"readiness"`
	MemoryMB   int               `json:"memory_mb,omitempty"`
	IPOffset   int               `json:"ip_offset"`
	Initialize []Initialization  `json:"initialize,omitempty"`
}

// Initialization runs once in the selected component before business startup.
// Stdin is a local input file; infrastructure resolves and snapshots it.
type Initialization struct {
	Name           string   `json:"name"`
	Command        []string `json:"command"`
	Stdin          string   `json:"stdin,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}
