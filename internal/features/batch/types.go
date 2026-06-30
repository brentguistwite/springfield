package batch

import (
	"encoding/json"
	"time"
)

// PhaseMode controls whether plans in a phase run serially or in parallel.
type PhaseMode string

const (
	PhaseSerial   PhaseMode = "serial"
	PhaseParallel PhaseMode = "parallel"
)

// Phase groups plan IDs that share an execution mode.
type Phase struct {
	Mode   PhaseMode       `json:"mode"`
	Plans  []string        `json:"plans"`            // ordered plan IDs
	Slices json.RawMessage `json:"slices,omitempty"` // legacy field — must be absent
}

// Batch is the compile-time state for one Springfield work batch.
type Batch struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Phases []Phase `json:"phases"`
	// PlanIDs is the ordered-unique union of all plan IDs referenced across
	// phases[].plans, preserving first-seen order. Derived by Compile; callers
	// must not mutate it. Used as the canonical superset for fast existence checks.
	PlanIDs []string `json:"plan_ids"`
}

// Run is the runtime-only cursor state for the active batch.
type Run struct {
	ActiveBatchID  string    `json:"active_batch_id"`
	ActivePlanIDs  []string  `json:"active_plan_ids,omitempty"`
	LastCheckpoint time.Time `json:"last_checkpoint,omitempty"`
	// FatalError is set only on terminal failure that requires user intervention.
	// Recoverable errors (agent retries, transient plan failures that will resume)
	// are appended to LastRetry instead so FatalError stays a reliable post-mortem signal.
	FatalError string   `json:"fatal_error,omitempty"`
	LastRetry  []string `json:"last_retry,omitempty"`
	// OriginalBranch records the branch the operator was on before the
	// auto-branch flow cut a feature branch. Empty when auto-branching did
	// not fire. Used to switch back when the batch terminates and to detect
	// resume when a prior interrupted run already auto-cut.
	OriginalBranch string `json:"original_branch,omitempty"`
	// AutoBranchName records the auto-cut feature branch the batch runs on.
	// Empty when auto-branching did not fire.
	AutoBranchName string `json:"auto_branch_name,omitempty"`
	// CostCapped is set when a batch was paused because total spend reached
	// or exceeded --cost-cap. Distinct from FatalError: cost-capped is a
	// resumable pause (rerun with a higher --cost-cap to continue), not a
	// terminal failure requiring `springfield recover`.
	CostCapped bool `json:"cost_capped,omitempty"`
	// BatchMode is the branch-output mode ("consolidate" | "per-plan") chosen
	// when the batch first started. Stamped ONCE before the first plan runs
	// and never rewritten on resume — it is authoritative on resume so a
	// re-passed --per-plan-branches cannot flip a batch mid-flight (which would
	// merge the front half and retain the back half). Empty marks a batch that
	// predates per-plan mode (treated as consolidate) or a not-yet-stamped run.
	BatchMode string `json:"batch_mode,omitempty"`
	// BatchBase is the resolved batch-wide base ref stamped alongside BatchMode
	// on the first start, as a resume FALLBACK only (never rewritten). On resume
	// a re-passed --base wins over it; per-plan PlanState.BaseRef remains the
	// authoritative reporting source. Empty in consolidate mode (base resolves
	// per-plan, post-autobranch, as before).
	BatchBase string `json:"batch_base,omitempty"`
}

const maxLastRetry = 10

// AppendRetry records a recoverable error onto the retry log (capped).
func (r *Run) AppendRetry(msg string) {
	if msg == "" {
		return
	}
	r.LastRetry = append(r.LastRetry, msg)
	if len(r.LastRetry) > maxLastRetry {
		r.LastRetry = r.LastRetry[len(r.LastRetry)-maxLastRetry:]
	}
}

// ArchiveEntry is the compact summary stored after a batch completes or is replaced.
//
// TotalUSD and CostBreakdown are populated at archive time when the caller
// can compute a cost.Rollup for the batch (start completion, plan-replace).
// Legacy archives written before cost capture landed have these zero and
// must be skipped when computing historical estimates rather than treated
// as $0 batches.
type ArchiveEntry struct {
	BatchID       string             `json:"batch_id"`
	Title         string             `json:"title"`
	ArchivedAt    time.Time          `json:"archived_at"`
	Reason        string             `json:"reason,omitempty"`
	Plans         []ArchivePlan      `json:"plans,omitempty"`
	TotalUSD      float64            `json:"total_usd,omitempty"`
	CostBreakdown map[string]float64 `json:"cost_breakdown,omitempty"`
}

// ArchivePlan is the per-plan summary in an archive entry.
// Status is a freeform string carried for archive forensics.
//
// Branch/BaseRef/EvidencePath capture the per-ticket trail (branch ↔ ticket ↔
// base ↔ durable evidence) so it survives the live PlanState being cleared at
// teardown — the only place this data lives once the batch is archived. All
// three are omitempty so legacy 3-field archives deserialize with them zeroed.
type ArchivePlan struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Branch is the plan's springfield/<plan> branch (preserved in per-plan
	// mode; deleted in consolidate mode, so empty there).
	Branch string `json:"branch,omitempty"`
	// BaseRef is the base the plan branched from (the authoritative per-plan
	// base, snapshotted from PlanState before teardown).
	BaseRef string `json:"base_ref,omitempty"`
	// EvidencePath is the durable, project-relative path the plan's execution
	// evidence was relocated to under .springfield/archive/<batchID>/plans/.
	// Empty when the plan produced no evidence.
	EvidencePath string `json:"evidence_path,omitempty"`
}
