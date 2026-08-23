// Package statusview defines the stable JSON view-model emitted by
// `springfield status --json`. The exported types here ARE the contract:
// internal state (conductor/batch/cost) is projected into them, never
// embedded. See the design spec for the field source map and the status
// enum mapping.
package statusview

import "time"

// SchemaVersion is the contract version. Adding fields is a non-breaking
// bump; removing/renaming requires an increment so consumers can branch.
// v2 added the per-plan in-flight [ActivityView] card (additive).
// v3 added the per-plan [StallView] possibly-wedged indicator (additive).
// v4 added the batch-level [RetroView] summary for archived batches (additive).
const SchemaVersion = 4

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
	// Retro is the batch-level retrospective digest, present only for an archived
	// batch that carries a readable retro.json. It is explicit null everywhere
	// else — an active/idle/orphan batch has no retro yet, and an archived batch
	// whose retro.json is absent or corrupt degrades to silence rather than error.
	Retro *RetroView `json:"retro"`
}

// RetroView is the compact retrospective summary surfaced for an archived batch,
// sourced from .springfield/archive/<batch-id>/retro.json. It is a digest, not
// the full report: the total finding count plus the most-prominent pattern key
// and how many plans tripped it, so a consumer sees "what went wrong most" at a
// glance and reads retro.json directly for the rest.
type RetroView struct {
	Findings   int    `json:"findings"`
	TopPattern string `json:"top_pattern,omitempty"`
	// TopCount is how many plans tripped TopPattern; omitted when zero (a
	// batch-level finding with no plans) so a consumer never renders "x0".
	TopCount int `json:"top_count,omitempty"`
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
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	Branch       string     `json:"branch"`
	BaseBranch   string     `json:"base_branch"`
	BaseHead     string     `json:"base_head"`
	Verify       VerifyView `json:"verify"`
	Review       ReviewView `json:"review"`
	Attempt      int        `json:"attempt"`
	LastError    *string    `json:"last_error"`
	EvidencePath string     `json:"evidence_path"`
	// Agent is the adapter running the plan (claude / codex / gemini), surfaced
	// so a live watcher shows which agent holds the plan. Empty (omitted) when
	// the plan has no recorded agent yet.
	Agent string `json:"agent,omitempty"`
	// StartedAt is when the current attempt began; nil (omitted) until the plan
	// starts. A live watcher renders elapsed time from it — sourced here so the
	// watch and --json surfaces read the same durable timestamp, never a second
	// clock.
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	Merge       MergeView       `json:"merge"`
	Integration IntegrationView `json:"integration"`
	// Activity is the in-flight progress card. It is explicit null for any plan
	// that is not running — the contract degrades to silence rather than show a
	// stale phase (see [ActivityView]).
	Activity *ActivityView `json:"activity"`
	// Stall is the possibly-wedged indicator: non-null only while a running plan's
	// agent has been classified silent past the configured stall threshold. It is
	// explicit null otherwise (not running, or running normally). Advisory only —
	// a wedge never kills the subprocess (see [StallView]).
	Stall *StallView `json:"stall"`
}

// StallView is the per-plan possibly-wedged card: a running plan whose agent
// emitted no stream event within the configured stall threshold. stale_for is
// the human-readable threshold crossed; since stamps when the wedge was first
// observed; occurrences counts distinct idle stretches flagged this run (> 1
// means the plan recovered and re-wedged). It is surfaced ONLY while the plan is
// running so a stale signal from a prior run can never leak.
type StallView struct {
	StaleFor    string    `json:"stale_for"`
	Since       time.Time `json:"since"`
	Occurrences int       `json:"occurrences"`
}

// ActivityView is the in-flight progress card: what a running plan is doing
// right now. It is null for any plan not in the running state — a stale phase
// is worse than none, so the projection drops it off the running path. phase is
// the coarse lifecycle stage (implementing / reviewing / verifying / merging);
// detail is an optional human phrase; round is the per-phase fine counter.
type ActivityView struct {
	Phase     string    `json:"phase"`
	Detail    string    `json:"detail,omitempty"`
	Round     int       `json:"round,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
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

// VerifyView is always present; verdict/reason are null unless the plan halted
// at the objective verify gate (a failing verify command exhausted the fix
// loop). It mirrors ReviewView so a --json consumer can tell a verify halt
// (fix the failing command) from a review halt (address findings) rather than
// inferring intent from last_error.
type VerifyView struct {
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
