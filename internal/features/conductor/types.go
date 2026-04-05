package conductor

// Config represents persisted conductor configuration.
type Config struct {
	PlansDir        string     `json:"plans_dir"`
	WorktreeBase    string     `json:"worktree_base"`
	MaxRetries      int        `json:"max_retries"`
	RalphIterations int        `json:"ralph_iterations"`
	RalphTimeout    int        `json:"ralph_timeout"`
	Tool            string     `json:"tool"`
	FallbackTool    string     `json:"fallback_tool,omitempty"`
	Batches         [][]string `json:"batches"`
	Sequential      []string   `json:"sequential"`
}

// PlanStatus describes conductor state for one plan.
type PlanStatus string

const (
	StatusPending   PlanStatus = "pending"
	StatusRunning   PlanStatus = "running"
	StatusCompleted PlanStatus = "completed"
	StatusFailed    PlanStatus = "failed"
)

// PlanState tracks status and optional failure detail for a single plan.
type PlanState struct {
	Status PlanStatus `json:"status"`
	Error  string     `json:"error,omitempty"`
}

// State represents persisted conductor plan state.
type State struct {
	Plans map[string]*PlanState `json:"plans"`
}

// NewState builds an empty conductor state.
func NewState() *State {
	return &State{Plans: make(map[string]*PlanState)}
}
