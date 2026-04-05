package doctor

import "springfield/internal/core/agents"

// CheckStatus describes the outcome of a single agent check.
type CheckStatus string

const (
	StatusHealthy   CheckStatus = "healthy"
	StatusMissing   CheckStatus = "missing"
	StatusUnhealthy CheckStatus = "unhealthy"
)

// Check is the result of checking one agent's local availability.
type Check struct {
	AgentID  agents.ID
	Name     string
	Binary   string
	Path     string
	Status   CheckStatus
	Guidance string
}

// Report is the full doctor output across all registered agents.
type Report struct {
	Checks  []Check
	Healthy bool
	Summary string
}
