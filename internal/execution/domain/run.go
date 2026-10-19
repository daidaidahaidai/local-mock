package domain

import "fmt"

type State string

const (
	Starting State = "starting"
	Ready    State = "ready"
	Failed   State = "failed"
	Stopped  State = "stopped"
	Removed  State = "removed"
)

type Run struct {
	ID        string `json:"id"`
	Scenario  string `json:"scenario"`
	Project   string `json:"project"`
	Directory string `json:"directory"`
	State     State  `json:"state"`
	CreatedAt string `json:"created_at"`
	Error     string `json:"error,omitempty"`
}

func (r *Run) Transition(next State) error {
	allowed := map[State]map[State]bool{
		Starting: {Ready: true, Failed: true, Removed: true},
		Ready:    {Stopped: true, Removed: true},
		Failed:   {Stopped: true, Removed: true},
		Stopped:  {Stopped: true, Removed: true},
		Removed:  {Removed: true},
	}
	if !allowed[r.State][next] {
		return fmt.Errorf("invalid run transition %s -> %s", r.State, next)
	}
	r.State = next
	return nil
}
