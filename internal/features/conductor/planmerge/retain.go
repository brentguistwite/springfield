package planmerge

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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
	// Teardown, when non-nil, is invoked with the execution worktree path
	// immediately before that worktree is removed (same contract as
	// IntegrateInput.Teardown): best-effort release of out-of-worktree
	// resources while the worktree still exists. nil skips teardown.
	Teardown func(worktreePath string)
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
	// Retain runs concurrently across plans in a parallel phase; all state
	// access goes through the locked Project API (detached read + closure
	// write) and the shared-repo worktree removal takes the git lock.
	state, ok := in.Project.ReadPlan(in.PlanID)
	if !ok {
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
	// Idempotent re-entry: a prior Retain may have removed the worktree but then
	// failed to persist (a SaveState error), so a resume re-runs this with the
	// path already gone. Re-attempting WorktreeRemoveForce on an absent path
	// errors and would falsely report CleanupFailed — deadlocking the batch the
	// same way the merge path's retryArtifactRemove avoids. Treat an
	// already-absent worktree as a successful removal.
	if _, statErr := os.Stat(state.WorktreePath); errors.Is(statErr, fs.ErrNotExist) {
		progress(in.Progress, "retain %s: execution worktree already removed\n", in.PlanID)
	} else {
		// Teardown before removal, and only when the worktree still exists (the
		// already-removed branch above skips it), so a resume after a prior clean
		// removal does not re-tear-down. Best-effort — the hook cannot fail the
		// removal or the cleanup.
		if in.Teardown != nil {
			in.Teardown(state.WorktreePath)
		}
		// `git worktree remove` mutates shared .git metadata — serialize
		// against concurrent worktree add/remove from sibling plans.
		var removeErr error
		conductor.WithGitLock(func() {
			removeErr = in.Git.WorktreeRemoveForce(in.ControlRoot, state.WorktreePath)
		})
		if removeErr != nil {
			execCleanup.Status = conductor.CleanupFailed
			execCleanup.Error = removeErr.Error()
			cleanup.Status = conductor.CleanupFailed
			progress(in.Progress, "retain %s: execution worktree removal failed (branch preserved): %v\n", in.PlanID, removeErr)
		}
	}
	cleanup.ExecutionWorktree = execCleanup

	merge := &conductor.MergeOutcome{
		Status:      conductor.MergeSucceeded,
		Mode:        ModeStandalone,
		Reason:      ReasonStandaloneRetained,
		TargetRef:   state.BaseRef,
		AttemptedAt: now(),
	}
	in.Project.UpdatePlan(in.PlanID, func(ps *conductor.PlanState) {
		ps.Merge = merge
		ps.Cleanup = cleanup
	})

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
