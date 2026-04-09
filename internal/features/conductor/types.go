package conductor

import (
	"encoding/json"
	"time"
)

// Config represents persisted conductor configuration.
type Config struct {
	PlansDir                   string     `json:"plans_dir"`
	WorktreeBase               string     `json:"worktree_base"`
	MaxRetries                 int        `json:"max_retries"`
	SingleWorkstreamIterations int        `json:"single_workstream_iterations"`
	SingleWorkstreamTimeout    int        `json:"single_workstream_timeout"`
	Tool                       string     `json:"tool"`
	FallbackTool               string     `json:"fallback_tool,omitempty"`
	Batches                    [][]string `json:"batches"`
	Sequential                 []string   `json:"sequential"`
}

// UnmarshalJSON accepts both Springfield-owned and legacy persisted config keys.
func (c *Config) UnmarshalJSON(data []byte) error {
	type rawConfig struct {
		PlansDir                   string     `json:"plans_dir"`
		WorktreeBase               string     `json:"worktree_base"`
		MaxRetries                 int        `json:"max_retries"`
		SingleWorkstreamIterations *int       `json:"single_workstream_iterations"`
		SingleWorkstreamTimeout    *int       `json:"single_workstream_timeout"`
		RalphIterations            *int       `json:"ralph_iterations"`
		RalphTimeout               *int       `json:"ralph_timeout"`
		Tool                       string     `json:"tool"`
		FallbackTool               string     `json:"fallback_tool,omitempty"`
		Batches                    [][]string `json:"batches"`
		Sequential                 []string   `json:"sequential"`
	}

	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*c = Config{
		PlansDir:     raw.PlansDir,
		WorktreeBase: raw.WorktreeBase,
		MaxRetries:   raw.MaxRetries,
		Tool:         raw.Tool,
		FallbackTool: raw.FallbackTool,
		Batches:      raw.Batches,
		Sequential:   raw.Sequential,
	}
	if raw.SingleWorkstreamIterations != nil {
		c.SingleWorkstreamIterations = *raw.SingleWorkstreamIterations
	} else if raw.RalphIterations != nil {
		c.SingleWorkstreamIterations = *raw.RalphIterations
	}
	if raw.SingleWorkstreamTimeout != nil {
		c.SingleWorkstreamTimeout = *raw.SingleWorkstreamTimeout
	} else if raw.RalphTimeout != nil {
		c.SingleWorkstreamTimeout = *raw.RalphTimeout
	}

	return nil
}

// PlanStatus describes conductor state for one plan.
type PlanStatus string

const (
	StatusPending   PlanStatus = "pending"
	StatusRunning   PlanStatus = "running"
	StatusCompleted PlanStatus = "completed"
	StatusFailed    PlanStatus = "failed"
)

// PlanState tracks execution status, timing, evidence, and failure detail for a single plan.
type PlanState struct {
	Status       PlanStatus `json:"status"`
	Error        string     `json:"error,omitempty"`
	Agent        string     `json:"agent,omitempty"`
	EvidencePath string     `json:"evidence_path,omitempty"`
	Attempts     int        `json:"attempts"`
	StartedAt    time.Time  `json:"started_at,omitempty"`
	EndedAt      time.Time  `json:"ended_at,omitempty"`
}

// State represents persisted conductor plan state.
type State struct {
	Plans map[string]*PlanState `json:"plans"`
}

// NewState builds an empty conductor state.
func NewState() *State {
	return &State{Plans: make(map[string]*PlanState)}
}
