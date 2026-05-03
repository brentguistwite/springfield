package conductor

import "time"

// Config represents persisted conductor configuration.
type Config struct {
	PlansDir                   string     `json:"plans_dir"`
	WorktreeBase               string     `json:"worktree_base"`
	MaxRetries                 int        `json:"max_retries"`
	SingleWorkstreamIterations int        `json:"single_workstream_iterations"`
	SingleWorkstreamTimeout    int        `json:"single_workstream_timeout"`
	Tool                       string     `json:"tool"`
	Batches                    [][]string `json:"batches"`
	Sequential                 []string   `json:"sequential"`
	// PlanUnits is the explicit sequential plan-unit registry. When non-empty,
	// it is the source of truth for execution order; Sequential/Batches are
	// kept only as a projection for legacy in-process consumers and ignored
	// by the scheduler.
	PlanUnits []PlanUnit `json:"plan_units,omitempty"`
}

// PlanUnit is one durable Springfield plan-unit registration.
// Mutable runtime data (status, attempts, timestamps, evidence, error) lives
// in [PlanState] under [State]; PlanUnit is config-only.
type PlanUnit struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Path is the canonical project-relative location of the plan source file.
	Path string `json:"path"`
	// Ref is the optional base ref the plan should branch from.
	Ref string `json:"ref,omitempty"`
	// PlanBranch is the optional explicit branch name for the plan worktree.
	PlanBranch string `json:"plan_branch,omitempty"`
	// Order is a 1-based execution order index. Unique within a config.
	Order int `json:"order"`
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
