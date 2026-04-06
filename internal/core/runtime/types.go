package runtime

import (
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
)

// Request describes what the runtime should execute.
type Request struct {
	AgentID agents.ID
	Prompt  string
	WorkDir string
	Timeout time.Duration
	OnEvent exec.EventHandler
}

// Status is the outcome of a runtime execution.
type Status string

const (
	StatusPassed Status = "passed"
	StatusFailed Status = "failed"
)

// Result is the outcome of a runtime execution.
type Result struct {
	Agent     agents.ID
	Status    Status
	ExitCode  int
	Events    []exec.Event
	Err       error
	StartedAt time.Time
	EndedAt   time.Time
}
