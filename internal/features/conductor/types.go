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
//
// Parity-2 fields capture truthful per-plan worktree identity so a resumed run
// can decide between honest reuse, refused reuse, and a fresh attempt:
//   - WorktreePath / Branch / BaseRef / BaseHead pin the isolated checkout.
//   - InputDigest pins the plan file + project guidance bytes used for the
//     first attempt; drift forces a fresh attempt instead of silent reuse.
//   - ExitReason carries a short structured tag for the most recent terminal
//     transition (e.g. "completed", "agent-failed", "preflight-dirty-source").
type PlanState struct {
	Status       PlanStatus `json:"status"`
	Error        string     `json:"error,omitempty"`
	Agent        string     `json:"agent,omitempty"`
	EvidencePath string     `json:"evidence_path,omitempty"`
	Attempts     int        `json:"attempts"`
	StartedAt    time.Time  `json:"started_at,omitempty"`
	EndedAt      time.Time  `json:"ended_at,omitempty"`

	WorktreePath string `json:"worktree_path,omitempty"`
	Branch       string `json:"branch,omitempty"`
	BaseRef      string `json:"base_ref,omitempty"`
	BaseHead     string `json:"base_head,omitempty"`
	InputDigest  string `json:"input_digest,omitempty"`
	ExitReason   string `json:"exit_reason,omitempty"`

	// PlanHead is the head of Branch after the agent finished. Empty until a
	// merge integration phase observes the plan worktree.
	PlanHead string `json:"plan_head,omitempty"`

	// Merge captures the outcome of the post-execution merge integration
	// phase. nil when no merge has been attempted (e.g. execution failed or
	// the slice predates merge integration).
	Merge *MergeOutcome `json:"merge,omitempty"`

	// Cleanup captures the per-artifact disposition of the execution worktree,
	// merge worktree, and plan branch after a merge attempt. nil until the
	// merge phase runs.
	Cleanup *CleanupOutcome `json:"cleanup,omitempty"`
}

// MergeStatus describes the outcome of a single-plan merge integration.
//
// "refused" is reserved for the strict first-pass policy: the merge target
// branch head no longer matches the plan's recorded base_head, so this slice
// does not silently merge onto a moved target.
type MergeStatus string

const (
	// MergePending marks a plan whose execution finished and is awaiting
	// merge integration. Set by planrun.SinglePlan as soon as the plan
	// transitions to StatusCompleted so a save failure mid-Integrate
	// leaves the durable record truthful — IsIntegrated reports false on
	// Pending and the next start re-enters the merge phase instead of
	// silently advancing past the plan.
	MergePending   MergeStatus = "pending"
	MergeRefused   MergeStatus = "refused"
	MergeSucceeded MergeStatus = "succeeded"
	MergeFailed    MergeStatus = "failed"
)

// CleanupStatus describes the disposition of one cleanup artifact (or the
// aggregate cleanup outcome).
//
//   - "succeeded": artifact was deleted cleanly.
//   - "failed":    deletion was attempted and errored; artifact remains on disk.
//   - "preserved": deletion was deliberately skipped (merge refused/failed).
//   - "skipped":   artifact never existed (e.g. merge worktree never created).
type CleanupStatus string

const (
	CleanupSucceeded CleanupStatus = "succeeded"
	CleanupFailed    CleanupStatus = "failed"
	CleanupPreserved CleanupStatus = "preserved"
	CleanupSkipped   CleanupStatus = "skipped"
)

// MergeOutcome is the persisted record of one merge integration attempt.
//
// Refs/SHAs are recorded explicitly so later resume / queue logic can decide
// honestly without re-deriving them from the source checkout's mutable state.
type MergeOutcome struct {
	Status        MergeStatus `json:"status"`
	Mode          string      `json:"mode,omitempty"`
	Reason        string      `json:"reason,omitempty"`
	Error         string      `json:"error,omitempty"`
	TargetRef     string      `json:"target_ref,omitempty"`
	TargetHead    string      `json:"target_head,omitempty"`
	PostMergeHead string      `json:"post_merge_head,omitempty"`
	WorktreePath  string      `json:"worktree_path,omitempty"`
	AttemptedAt   time.Time   `json:"attempted_at,omitempty"`

	// SourceSyncStatus records what happened to the source checkout's
	// working tree after a successful merge. The merge phase publishes the
	// new head via `git update-ref`, which advances refs/heads/<target>
	// without touching the source worktree or index. When the target
	// branch is the source checkout's currently-checked-out branch, the
	// integration phase additionally syncs the worktree so subsequent
	// IsDirty preflights do not see a false-positive diff.
	//
	// Values: "synced", "skipped" (target was a different branch),
	// "failed" (sync attempted but git refused — see SourceSyncError).
	SourceSyncStatus string `json:"source_sync_status,omitempty"`
	SourceSyncError  string `json:"source_sync_error,omitempty"`
}

// ArtifactCleanup records the disposition of one cleanup artifact.
type ArtifactCleanup struct {
	Status CleanupStatus `json:"status"`
	Path   string        `json:"path,omitempty"`
	Branch string        `json:"branch,omitempty"`
	Reason string        `json:"reason,omitempty"`
	Error  string        `json:"error,omitempty"`
}

// CleanupOutcome aggregates per-artifact cleanup status. Status is
// "succeeded" only when every applicable artifact reports succeeded;
// "failed" when any artifact reports failed; "skipped" when the merge phase
// did not produce a clean-success path.
type CleanupOutcome struct {
	Status            CleanupStatus    `json:"status"`
	MergeWorktree     *ArtifactCleanup `json:"merge_worktree,omitempty"`
	ExecutionWorktree *ArtifactCleanup `json:"execution_worktree,omitempty"`
	PlanBranch        *ArtifactCleanup `json:"plan_branch,omitempty"`
}

// IsIntegrated reports whether this plan is fully integrated and may be
// treated as queue-complete by callers (Schedule.NextPlans/IsComplete plus
// status renderers).
//
// Rules:
//
//   - Status must be Completed.
//   - When Merge is set (the plan went through merge integration), its
//     status must be MergeSucceeded.
//   - When Cleanup reports CleanupFailed, the plan is NOT integrated even
//     if Merge succeeded — preserved artifacts must remain visible until
//     resolved.
//   - When Merge is nil (legacy execution path that does not record merge
//     state), the plan is treated as integrated to preserve the prior
//     scheduler contract for non-PlanUnit flows.
func (s *PlanState) IsIntegrated() bool {
	if s == nil || s.Status != StatusCompleted {
		return false
	}
	if s.Merge != nil && s.Merge.Status != MergeSucceeded {
		return false
	}
	if s.Merge != nil && s.Merge.SourceSyncStatus == "failed" {
		// Source resync left the source checkout in a phantom-dirty
		// state. Until the operator resolves it, the next preflight
		// would attribute its dirty-source rejection to the wrong
		// plan; keep this plan flagged as not-integrated so the queue
		// surface points at the real owner.
		return false
	}
	if s.Cleanup != nil && s.Cleanup.Status == CleanupFailed {
		return false
	}
	return true
}

// State represents persisted conductor plan state.
type State struct {
	Plans map[string]*PlanState `json:"plans"`
}

// NewState builds an empty conductor state.
func NewState() *State {
	return &State{Plans: make(map[string]*PlanState)}
}
