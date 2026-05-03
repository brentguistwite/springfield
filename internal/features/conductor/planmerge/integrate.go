package planmerge

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"springfield/internal/features/conductor"
)

// MergeMode is the merge strategy planmerge supports.
//
// Slice-3 ships a single mode: ModeFFOnly. The plan branch is created from
// the recorded base_head and Integrate refuses on any target drift, so the
// plan branch is always a descendant of the merge target at attempt time —
// fast-forward is the minimal honest publish strategy. A merge-commit mode
// is deferred to a later slice; locking the surface to ff-only here keeps
// out a second code path that this slice's tests would not exercise.
type MergeMode string

const (
	ModeFFOnly MergeMode = "ff-only"
)

// Reason tags applied to MergeOutcome.Reason and IntegrateResult.Reason.
const (
	ReasonTargetDrift   = "target-drift"
	ReasonFFNotPossible = "ff-not-possible"
	ReasonMergeFailed   = "merge-failed"
	ReasonRefUpdate     = "ref-update-failed"
	ReasonHeadUnknown   = "plan-head-unknown"
	ReasonStateMissing  = "plan-state-missing"
	ReasonMergeOK       = "merge-ok"
)

// IntegrateInput collects everything Integrate needs to merge one plan.
type IntegrateInput struct {
	Project      *conductor.Project
	PlanID       string
	ControlRoot  string
	WorktreeBase string
	Git          Git
	// Now is injected for deterministic timestamps in tests; nil defaults
	// to time.Now.
	Now func() time.Time
	// Progress receives short human-readable lifecycle lines; nil discards.
	Progress io.Writer
}

// IntegrateResult summarizes what Integrate did and what state it persisted.
type IntegrateResult struct {
	PlanID  string
	Merge   *conductor.MergeOutcome
	Cleanup *conductor.CleanupOutcome
	// Err is set only when something prevented Integrate from running its
	// lifecycle (e.g. saving state failed). A merge that was refused or
	// failed at the git layer is recorded in Merge and is NOT an Err.
	Err error
	// Reason is a short structured tag for the terminal outcome
	// ("target-drift", "merge-ok", "ff-not-possible", ...).
	Reason string
}

// Integrate merges a plan branch into its recorded target via a dedicated
// detached merge worktree and applies the cleanup matrix.
//
// Strict policy:
//   - If the current target branch head no longer matches the plan's
//     recorded base_head, the merge is refused. Nothing is created or
//     cleaned up; the operator can resume from a truthful starting point.
//   - The merge worktree is always created at a path distinct from the
//     source checkout and the execution worktree.
//   - The fast-forward is published to the source target branch via
//     `git update-ref` with the expected old-value, so a concurrent
//     advance of the target between drift-check and publish is rejected
//     atomically by git.
//   - Cleanup deletes the merge worktree, execution worktree, and plan
//     branch only on a clean merge success. Any cleanup failure leaves
//     the affected artifact preserved for inspection.
func Integrate(in IntegrateInput) IntegrateResult {
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
	if state.Branch == "" || state.BaseRef == "" || state.BaseHead == "" || state.WorktreePath == "" {
		return IntegrateResult{PlanID: in.PlanID, Err: fmt.Errorf("plan %q is missing identity fields needed for merge", in.PlanID), Reason: ReasonStateMissing}
	}

	// Re-entry path: prior merge already succeeded but cleanup left
	// artifacts behind. Re-running Integrate must NOT redo the merge —
	// the recorded base_head no longer matches the now-advanced target,
	// so a re-merge would falsely flag drift. Retry cleanup only.
	if state.Merge != nil && state.Merge.Status == conductor.MergeSucceeded {
		progress(in.Progress, "merge %s: prior merge already succeeded; retrying cleanup\n", in.PlanID)
		cleanup := runCleanupMatrix(in, state, state.Merge.WorktreePath)
		state.Cleanup = cleanup
		saveErr := in.Project.SaveState()
		out := IntegrateResult{
			PlanID:  in.PlanID,
			Merge:   state.Merge,
			Cleanup: cleanup,
			Reason:  ReasonMergeOK,
		}
		if saveErr != nil {
			out.Err = fmt.Errorf("save state after cleanup retry: %w", saveErr)
			out.Reason = "state-save-failed"
		}
		return out
	}

	// Re-entry from prior refused/failed merge: a leftover merge worktree
	// at the recorded path would block `git worktree add` on the second
	// attempt. Best-effort remove before re-creating; a real removal
	// failure surfaces later when the new add fails with an actionable
	// error.
	if state.Merge != nil && state.Merge.WorktreePath != "" {
		_ = in.Git.WorktreeRemoveForce(in.ControlRoot, state.Merge.WorktreePath)
	}

	progress(in.Progress, "merge %s: capturing plan head\n", in.PlanID)
	planHead, err := in.Git.Head(state.WorktreePath)
	if err != nil {
		// Without plan_head we cannot honestly assert what we're publishing.
		return finalize(in, state, now,
			refused(state, "", ReasonHeadUnknown, fmt.Sprintf("cannot read plan head: %v", err), now()),
			ReasonHeadUnknown)
	}
	state.PlanHead = planHead

	targetRef := state.BaseRef
	progress(in.Progress, "merge %s: resolving target %s\n", in.PlanID, targetRef)
	currentTargetHead, err := in.Git.ResolveRef(in.ControlRoot, targetRef)
	if err != nil {
		merge := refused(state, "", ReasonTargetDrift,
			fmt.Sprintf("cannot resolve target ref %s: %v", targetRef, err), now())
		return finalize(in, state, now, merge, ReasonTargetDrift)
	}

	if currentTargetHead != state.BaseHead {
		// Strict policy: refuse rather than silently merging onto a moved
		// target. Recovery / re-anchor is a later slice.
		merge := refused(state, "", ReasonTargetDrift,
			fmt.Sprintf("target %s head %s no longer matches recorded base_head %s; refusing merge",
				targetRef, currentTargetHead, state.BaseHead), now())
		merge.TargetHead = currentTargetHead
		progress(in.Progress, "merge %s: refused (target drift)\n", in.PlanID)
		return finalize(in, state, now, merge, ReasonTargetDrift)
	}

	mergeWtPath := mergeWorktreePath(in.ControlRoot, in.WorktreeBase, in.PlanID)
	progress(in.Progress, "merge %s: creating merge worktree at %s\n", in.PlanID, mergeWtPath)
	if err := os.MkdirAll(filepath.Dir(mergeWtPath), 0o755); err != nil {
		merge := failedMerge(state, currentTargetHead, "", ReasonMergeFailed,
			fmt.Sprintf("cannot prepare merge worktree parent dir: %v", err), now())
		return finalize(in, state, now, merge, ReasonMergeFailed)
	}
	if err := in.Git.WorktreeAddDetached(in.ControlRoot, mergeWtPath, targetRef); err != nil {
		merge := failedMerge(state, currentTargetHead, "", ReasonMergeFailed,
			fmt.Sprintf("cannot create merge worktree: %v", err), now())
		return finalize(in, state, now, merge, ReasonMergeFailed)
	}

	progress(in.Progress, "merge %s: ff-only merging %s\n", in.PlanID, state.Branch)
	if err := in.Git.MergeFFOnly(mergeWtPath, state.Branch); err != nil {
		merge := failedMerge(state, currentTargetHead, mergeWtPath, ReasonFFNotPossible,
			fmt.Sprintf("ff-only merge of %s into %s failed: %v", state.Branch, targetRef, err), now())
		progress(in.Progress, "merge %s: failed — ff-only refused\n", in.PlanID)
		return finalize(in, state, now, merge, ReasonFFNotPossible)
	}

	mergedHead, err := in.Git.Head(mergeWtPath)
	if err != nil {
		merge := failedMerge(state, currentTargetHead, mergeWtPath, ReasonMergeFailed,
			fmt.Sprintf("cannot read merged head: %v", err), now())
		return finalize(in, state, now, merge, ReasonMergeFailed)
	}

	progress(in.Progress, "merge %s: publishing %s -> %s via update-ref\n", in.PlanID, mergedHead, targetRef)
	if err := in.Git.UpdateBranchRef(in.ControlRoot, targetRef, mergedHead, currentTargetHead); err != nil {
		// CAS lost: the target moved between drift check and publish.
		merge := failedMerge(state, currentTargetHead, mergeWtPath, ReasonRefUpdate,
			fmt.Sprintf("update-ref %s lost CAS: %v", targetRef, err), now())
		progress(in.Progress, "merge %s: failed — concurrent target advance\n", in.PlanID)
		return finalize(in, state, now, merge, ReasonRefUpdate)
	}

	merge := &conductor.MergeOutcome{
		Status:        conductor.MergeSucceeded,
		Mode:          string(ModeFFOnly),
		Reason:        ReasonMergeOK,
		TargetRef:     targetRef,
		TargetHead:    currentTargetHead,
		PostMergeHead: mergedHead,
		WorktreePath:  mergeWtPath,
		AttemptedAt:   now(),
	}

	// H1: source checkout resync. update-ref advanced refs/heads/<target>
	// without touching the source worktree or index. When the target
	// branch is the source checkout's current HEAD, the source's status
	// would now show every committed change as "uncommitted" because the
	// index/worktree still reflect the pre-merge state. Sync the source
	// to the new head so subsequent IsDirty preflights are honest.
	syncStatus, syncErr := resyncSourceCheckout(in.Git, in.ControlRoot, targetRef, mergedHead)
	merge.SourceSyncStatus = syncStatus
	if syncErr != nil {
		merge.SourceSyncError = syncErr.Error()
		progress(in.Progress, "merge %s: source resync failed — %v\n", in.PlanID, syncErr)
	}
	state.Merge = merge

	// H2: persist merge success BEFORE any destructive cleanup. If the
	// state save fails here, the merge has been published to the source
	// repo but the on-disk record would not reflect it; running cleanup
	// would then erase the only artifacts an operator could use to
	// reconstruct what happened. Abort cleanup, surface the save error,
	// and leave every artifact preserved.
	if err := in.Project.SaveState(); err != nil {
		return IntegrateResult{
			PlanID:  in.PlanID,
			Merge:   merge,
			Cleanup: nil,
			Err:     fmt.Errorf("save merge state before cleanup: %w", err),
			Reason:  "merge-state-save-failed",
		}
	}

	cleanup := runCleanupMatrix(in, state, mergeWtPath)
	state.Cleanup = cleanup
	if err := in.Project.SaveState(); err != nil {
		// Cleanup may have run; the affected artifacts are gone but we
		// could not record their disposition. Surface loudly: the merge
		// record on disk reflects success without the cleanup ledger.
		return IntegrateResult{
			PlanID:  in.PlanID,
			Merge:   merge,
			Cleanup: cleanup,
			Err:     fmt.Errorf("save cleanup state: %w", err),
			Reason:  "cleanup-state-save-failed",
		}
	}

	if cleanup.Status == conductor.CleanupFailed {
		progress(in.Progress, "merge %s: succeeded; cleanup failed (artifacts preserved)\n", in.PlanID)
	} else {
		progress(in.Progress, "merge %s: succeeded; cleanup ok\n", in.PlanID)
	}

	return IntegrateResult{
		PlanID:  in.PlanID,
		Merge:   merge,
		Cleanup: cleanup,
		Reason:  ReasonMergeOK,
	}
}

// resyncSourceCheckout brings the source checkout's worktree+index up to
// the post-merge head when the target branch is the source's current HEAD.
// Returns ("synced", nil) on success, ("skipped", nil) when the target
// belongs to a different branch (or HEAD is detached/unreadable), and
// ("failed", err) when the reset itself failed.
func resyncSourceCheckout(g Git, controlRoot, targetRef, newSHA string) (string, error) {
	cur, err := g.CurrentBranch(controlRoot)
	if err != nil {
		// Detached HEAD or unreadable branch: the source checkout was
		// never on the target branch in the first place, so there is
		// nothing to sync.
		return "skipped", nil
	}
	if cur != targetRef {
		return "skipped", nil
	}
	if err := g.ResetHard(controlRoot, newSHA); err != nil {
		return "failed", err
	}
	return "synced", nil
}

// finalize assigns merge + preservation cleanup to state and persists it.
// Used on every non-success terminal path so partial state never leaks to
// disk after the merge phase has decided.
func finalize(in IntegrateInput, state *conductor.PlanState, now func() time.Time, merge *conductor.MergeOutcome, reason string) IntegrateResult {
	cleanup := preserveAllCleanup(state, merge.WorktreePath, reason)
	state.Merge = merge
	state.Cleanup = cleanup
	saveErr := in.Project.SaveState()
	out := IntegrateResult{
		PlanID:  in.PlanID,
		Merge:   merge,
		Cleanup: cleanup,
		Reason:  reason,
	}
	if saveErr != nil {
		out.Err = fmt.Errorf("save state after %s: %w", reason, saveErr)
	}
	_ = now
	return out
}

// refused builds a MergeOutcome with Status=Refused.
func refused(state *conductor.PlanState, mergeWtPath, reason, msg string, when time.Time) *conductor.MergeOutcome {
	return &conductor.MergeOutcome{
		Status:       conductor.MergeRefused,
		Mode:         string(ModeFFOnly),
		Reason:       reason,
		Error:        msg,
		TargetRef:    state.BaseRef,
		WorktreePath: mergeWtPath,
		AttemptedAt:  when,
	}
}

// failedMerge builds a MergeOutcome with Status=Failed.
func failedMerge(state *conductor.PlanState, targetHead, mergeWtPath, reason, msg string, when time.Time) *conductor.MergeOutcome {
	return &conductor.MergeOutcome{
		Status:       conductor.MergeFailed,
		Mode:         string(ModeFFOnly),
		Reason:       reason,
		Error:        msg,
		TargetRef:    state.BaseRef,
		TargetHead:   targetHead,
		WorktreePath: mergeWtPath,
		AttemptedAt:  when,
	}
}

// preserveAllCleanup returns a CleanupOutcome that marks every artifact as
// preserved with reason. mergeWtPath is empty when no merge worktree was
// created (a pre-create refusal path).
func preserveAllCleanup(state *conductor.PlanState, mergeWtPath, reason string) *conductor.CleanupOutcome {
	out := &conductor.CleanupOutcome{Status: conductor.CleanupSkipped}
	if mergeWtPath != "" {
		out.MergeWorktree = &conductor.ArtifactCleanup{
			Status: conductor.CleanupPreserved,
			Path:   mergeWtPath,
			Reason: reason,
		}
	} else {
		out.MergeWorktree = &conductor.ArtifactCleanup{
			Status: conductor.CleanupSkipped,
			Reason: "merge worktree never created",
		}
	}
	out.ExecutionWorktree = &conductor.ArtifactCleanup{
		Status: conductor.CleanupPreserved,
		Path:   state.WorktreePath,
		Reason: reason,
	}
	out.PlanBranch = &conductor.ArtifactCleanup{
		Status: conductor.CleanupPreserved,
		Branch: state.Branch,
		Reason: reason,
	}
	return out
}

// runCleanupMatrix attempts to delete the merge worktree, the execution
// worktree, and the plan branch. Each artifact is tracked independently.
// Aggregate Status is "succeeded" when every artifact deleted cleanly,
// "failed" if any deletion errored.
func runCleanupMatrix(in IntegrateInput, state *conductor.PlanState, mergeWtPath string) *conductor.CleanupOutcome {
	out := &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded}

	mw := &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Path: mergeWtPath}
	if err := in.Git.WorktreeRemoveForce(in.ControlRoot, mergeWtPath); err != nil {
		mw.Status = conductor.CleanupFailed
		mw.Error = err.Error()
		out.Status = conductor.CleanupFailed
	}
	out.MergeWorktree = mw

	xw := &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Path: state.WorktreePath}
	if err := in.Git.WorktreeRemoveForce(in.ControlRoot, state.WorktreePath); err != nil {
		xw.Status = conductor.CleanupFailed
		xw.Error = err.Error()
		out.Status = conductor.CleanupFailed
	}
	out.ExecutionWorktree = xw

	pb := &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Branch: state.Branch}
	if err := in.Git.BranchDelete(in.ControlRoot, state.Branch); err != nil {
		pb.Status = conductor.CleanupFailed
		pb.Error = err.Error()
		out.Status = conductor.CleanupFailed
	}
	out.PlanBranch = pb

	return out
}

// mergeWorktreeSubdir is the dotfile subdirectory under WorktreeBase that
// holds merge worktrees. The leading dot guarantees no collision with plan
// keys: PlanUnit IDs are sanitized to [a-z0-9-] (see batch.SanitizeID), so
// no plan worktree can ever land inside this directory.
const mergeWorktreeSubdir = ".merges"

// mergeWorktreePath returns an absolute path for a plan's merge worktree.
// The path is under WorktreeBase but inside a dedicated [mergeWorktreeSubdir]
// directory so a plan ID like "merge-alpha" cannot collide with another
// plan's merge worktree the way a flat "merge-<id>" prefix would.
func mergeWorktreePath(controlRoot, worktreeBase, planID string) string {
	if worktreeBase == "" {
		worktreeBase = ".worktrees"
	}
	base := worktreeBase
	if !filepath.IsAbs(base) {
		base = filepath.Join(controlRoot, worktreeBase)
	}
	return filepath.Clean(filepath.Join(base, mergeWorktreeSubdir, planID))
}

// progress writes a short status line when w is non-nil. Mirrors the helper
// in planrun so the two phases produce a consistent surface.
func progress(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}

// IsRefused reports whether result records a refused merge. Convenience for
// callers that need to branch on outcome without inspecting Merge.Status.
func IsRefused(result IntegrateResult) bool {
	return result.Merge != nil && result.Merge.Status == conductor.MergeRefused
}

// IsSuccess reports whether result records a successful merge. Cleanup may
// still have failed; check Cleanup.Status separately.
func IsSuccess(result IntegrateResult) bool {
	return result.Err == nil && result.Merge != nil && result.Merge.Status == conductor.MergeSucceeded
}
