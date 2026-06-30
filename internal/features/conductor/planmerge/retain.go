package planmerge

import (
	"fmt"
	"io"
	"time"

	"springfield/internal/features/conductor"
)

// ModeStandalone is the MergeOutcome.Mode recorded for a plan completed in
// per-plan branch mode: no merge happened, the branch is retained as-is for
// the operator to open a PR from.
const ModeStandalone = "standalone"

// ReasonStandaloneRetained tags the merge + cleanup records a standalone
// completion writes, so status renderers can distinguish a retained branch
// from a merged-and-deleted one.
const ReasonStandaloneRetained = "standalone-branch-retained"

// RetainInput collects everything Retain needs to finalize one plan in
// per-plan branch mode.
type RetainInput struct {
	Project     *conductor.Project
	PlanID      string
	ControlRoot string
	Git         Git
	// Now is injected for deterministic timestamps in tests; nil defaults to
	// time.Now.
	Now func() time.Time
	// Progress receives short human-readable lifecycle lines; nil discards.
	Progress io.Writer
}

// Retain finalizes a clean-success plan in per-plan branch mode: it skips the
// merge entirely, removes the execution worktree, and KEEPS the plan branch so
// the operator can open one PR per plan.
//
// This is deliberately separate from [Integrate] rather than a mode flag on it
// — the merge path's drift/CAS/recovery machinery is battle-tested and stays
// untouched. Retain must NOT call runCleanupMatrixWithPrior: that helper
// unconditionally `git branch -D`s the plan branch, which is exactly what
// per-plan mode must not do. The CleanupOutcome is therefore hand-built:
//
//   - MergeWorktree: skipped (none is ever created in standalone mode).
//   - ExecutionWorktree: removed directly; succeeded or failed.
//   - PlanBranch: preserved, carrying the branch name for the operator.
//
// On a clean removal the recorded Merge/Cleanup satisfy
// [conductor.PlanState.IsIntegrated], so the batch scheduler advances past the
// plan. A worktree-remove failure records ExecutionWorktree=failed (aggregate
// cleanup failed → not integrated) while still preserving the branch.
func Retain(in RetainInput) IntegrateResult {
	now := in.Now
	if now == nil {
		now = time.Now
	}
	if in.Project == nil {
		return IntegrateResult{PlanID: in.PlanID, Err: fmt.Errorf("project is required"), Reason: ReasonStateMissing}
	}
	if in.Git == nil {
		in.Git = CLIGit{}
	}
	state, ok := in.Project.State.Plans[in.PlanID]
	if !ok || state == nil {
		return IntegrateResult{PlanID: in.PlanID, Err: fmt.Errorf("no plan state recorded for %q", in.PlanID), Reason: ReasonStateMissing}
	}
	if state.Branch == "" || state.WorktreePath == "" {
		return IntegrateResult{PlanID: in.PlanID, Err: fmt.Errorf("plan %q is missing identity fields needed for retain", in.PlanID), Reason: ReasonStateMissing}
	}
	// Retain records a terminal MergeSucceeded; only a plan that actually
	// finished execution may earn that. Refuse otherwise rather than stamping a
	// fraudulent success a caller bug could later read as integrated.
	if state.Status != conductor.StatusCompleted {
		return IntegrateResult{PlanID: in.PlanID, Err: fmt.Errorf("retain requires plan %q to be completed, got %q", in.PlanID, state.Status), Reason: ReasonStateMissing}
	}

	progress(in.Progress, "retain %s: keeping standalone branch %s; removing execution worktree\n", in.PlanID, state.Branch)

	cleanup := &conductor.CleanupOutcome{
		Status: conductor.CleanupSucceeded,
		MergeWorktree: &conductor.ArtifactCleanup{
			Status: conductor.CleanupSkipped,
			Reason: "no merge in per-plan branch mode",
		},
		PlanBranch: &conductor.ArtifactCleanup{
			Status: conductor.CleanupPreserved,
			Branch: state.Branch,
			Reason: ReasonStandaloneRetained,
		},
	}

	execCleanup := &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Path: state.WorktreePath}
	if err := in.Git.WorktreeRemoveForce(in.ControlRoot, state.WorktreePath); err != nil {
		execCleanup.Status = conductor.CleanupFailed
		execCleanup.Error = err.Error()
		cleanup.Status = conductor.CleanupFailed
		progress(in.Progress, "retain %s: execution worktree removal failed (branch preserved): %v\n", in.PlanID, err)
	}
	cleanup.ExecutionWorktree = execCleanup

	merge := &conductor.MergeOutcome{
		Status:      conductor.MergeSucceeded,
		Mode:        ModeStandalone,
		Reason:      ReasonStandaloneRetained,
		TargetRef:   state.BaseRef,
		AttemptedAt: now(),
	}
	state.Merge = merge
	state.Cleanup = cleanup

	if err := in.Project.SaveState(); err != nil {
		return IntegrateResult{
			PlanID:  in.PlanID,
			Merge:   merge,
			Cleanup: cleanup,
			Err:     fmt.Errorf("save retain state: %w", err),
			Reason:  "retain-state-save-failed",
		}
	}

	return IntegrateResult{
		PlanID:  in.PlanID,
		Merge:   merge,
		Cleanup: cleanup,
		Reason:  ReasonStandaloneRetained,
	}
}
