// Package statusview defines the stable JSON view-model emitted by
// `springfield status --json`. The exported types here ARE the contract:
// internal state (conductor/batch/cost) is projected into them, never
// embedded. See the design spec for the field source map and the status
// enum mapping.
package statusview

// SchemaVersion is the contract version. Adding fields is a non-breaking
// bump; removing/renaming requires an increment so consumers can branch.
const SchemaVersion = 1

// Public board-status enum. A total projection of conductor.PlanStatus
// composed with merge outcome.
const (
	StatusPending    = "pending"
	StatusRunning    = "running"
	StatusStalled    = "stalled" // started but no live process owns the control-plane lock — needs resume/abandon
	StatusNeedsHuman = "needs-human"
	StatusFailed     = "failed"
	StatusDone       = "done"     // completed, not merged
	StatusMerged     = "merged"   // completed + merge succeeded
	StatusRetained   = "retained" // archived per-plan: completed, branch kept for a standalone PR (never merged into a base)
)

// View is the top-level envelope. Absent sections are explicit null.
type View struct {
	SchemaVersion int           `json:"schema_version"`
	State         string        `json:"state"` // "active" | "orphan" | "idle" | "archived"
	Summary       string        `json:"summary"`
	Batch         *BatchView    `json:"batch"`
	Progress      *ProgressView `json:"progress"`
	Spend         *SpendView    `json:"spend"`
	Flags         *FlagsView    `json:"flags"`
	Plans         []PlanView    `json:"plans"`
}

type BatchView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type ProgressView struct {
	Completed        int  `json:"completed"`
	Total            int  `json:"total"`
	PhaseIndex       int  `json:"phase_index"`
	PhaseTotal       int  `json:"phase_total"`
	AllDone          bool `json:"all_done"`
	ParallelInFlight bool `json:"parallel_in_flight"`
}

type SpendView struct {
	TotalUSD     float64            `json:"total_usd"`
	PerAdapter   map[string]float64 `json:"per_adapter,omitempty"`
	Iterations   int                `json:"iterations"`
	UnpricedRuns int                `json:"unpriced_runs,omitempty"`
	// SkippedFiles is non-zero when some cost.json files could not be read;
	// a non-zero value means total_usd may under-count actual spend.
	SkippedFiles int `json:"skipped_files,omitempty"`
}

type FlagsView struct {
	FatalError *string  `json:"fatal_error"`
	CostCapped bool     `json:"cost_capped"`
	LastRetry  []string `json:"last_retry,omitempty"`
}

// PlanView is the deliberately-projected per-plan card. Every field maps to
// persisted PlanState/PlanUnit; see the spec's field source map.
type PlanView struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Status       string          `json:"status"`
	Branch       string          `json:"branch"`
	BaseBranch   string          `json:"base_branch"`
	BaseHead     string          `json:"base_head"`
	Review       ReviewView      `json:"review"`
	Attempt      int             `json:"attempt"`
	LastError    *string         `json:"last_error"`
	EvidencePath string          `json:"evidence_path"`
	Merge        MergeView       `json:"merge"`
	Integration  IntegrationView `json:"integration"`
}

// IntegrationView is a rollup of post-merge disposition so a consumer can
// trust one field instead of AND-ing merge/cleanup/source-sync primitives.
// state is "needs_attention" when a plan's merge succeeded but the plan is
// NOT integrated (cleanup or source-sync failed) — a stuck plan that
// merge.status:"succeeded" alone would mask. Otherwise "clean".
type IntegrationView struct {
	State  string  `json:"state"`            // "clean" | "needs_attention"
	Reason *string `json:"reason,omitempty"` // "cleanup-failed" | "source-sync-failed" when needs_attention
}

// ReviewView is always present; verdict/reason are null unless the plan
// halted at the pre-merge review gate.
type ReviewView struct {
	Verdict *string `json:"verdict"`
	Reason  *string `json:"reason"`
}

// MergeView is always present; status/reason/error null until a merge is attempted.
// reason is a machine slug (e.g. "drift-detected"); error is the human-readable
// detail (e.g. the raw git error) and is populated only when non-empty.
type MergeView struct {
	Status *string `json:"status"`
	Reason *string `json:"reason"`
	Error  *string `json:"error"`
}
