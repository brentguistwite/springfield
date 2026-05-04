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
	// so a re-merge would falsely flag drift. Retry cleanup only, and
	// carry forward any artifact that already deleted cleanly so a
	// second `git worktree remove` against an already-removed path does
	// not falsely re-fail the cleanup status.
	if state.Merge != nil && state.Merge.Status == conductor.MergeSucceeded {
		progress(in.Progress, "merge %s: prior merge already succeeded; retrying resync+cleanup as needed\n", in.PlanID)
		// finishSuccessfulMerge always runs resyncSourceCheckout, so
		// retrying a failed sync is just a normal re-entry into that
		// path — no separate retry call needed (a redundant retry could
		// race with a user edit between the two calls).
		return finishSuccessfulMerge(in, state, now, state.Merge)
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

	// Re-entry recovery: a prior Integrate may have committed update-ref
	// but failed the post-publish SaveState. The durable Merge record
	// still says Pending with the intended PostMergeHead recorded
	// (persisted just before update-ref); the target ref already points
	// at that head. Without this branch the next run would observe
	// target_head != base_head, refuse with target-drift, and leave the
	// plan permanently stuck on a transient write failure.
	if state.Merge != nil &&
		state.Merge.Status == conductor.MergePending &&
		state.Merge.PostMergeHead != "" &&
		currentTargetHead == state.Merge.PostMergeHead {
		progress(in.Progress, "merge %s: prior publish detected (target at recorded post_merge_head); resuming as succeeded\n", in.PlanID)
		// Promote to Succeeded preserving the recorded refs/SHAs; the
		// SHAs were written before publish so they accurately describe
		// what landed.
		merge := *state.Merge
		merge.Status = conductor.MergeSucceeded
		merge.Reason = ReasonMergeOK
		merge.Error = ""
		state.Merge = &merge
		return finishSuccessfulMerge(in, state, now, &merge)
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
	// Re-entry from a prior refused/failed merge may have preserved a
	// merge worktree at this path. Remove ONLY now — after every preflight
	// (planHead, drift, recovery) has passed — so a fresh refusal cannot
	// destroy the prior preserved artifact before a viable new attempt.
	if state.Merge != nil && state.Merge.WorktreePath != "" {
		_ = in.Git.WorktreeRemoveForce(in.ControlRoot, state.Merge.WorktreePath)
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

	// Persist publish-intent BEFORE update-ref. If update-ref succeeds
	// but the post-publish SaveState fails, the durable record carries
	// PostMergeHead so the next Integrate can detect "publish already
	// landed" by comparing target_head against the recorded value. The
	// status stays Pending until the publish is confirmed and the
	// post-publish save lands.
	state.Merge = &conductor.MergeOutcome{
		Status:        conductor.MergePending,
		Mode:          string(ModeFFOnly),
		TargetRef:     targetRef,
		TargetHead:    currentTargetHead,
		PostMergeHead: mergedHead,
		WorktreePath:  mergeWtPath,
		AttemptedAt:   now(),
	}
	if err := in.Project.SaveState(); err != nil {
		// Pre-publish save failure: nothing has been published; treat
		// as a merge failure so the merge worktree stays preserved
		// and the operator can inspect.
		merge := failedMerge(state, currentTargetHead, mergeWtPath, ReasonMergeFailed,
			fmt.Sprintf("save publish intent: %v", err), now())
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

	// Promote in-memory record to Succeeded; reuse the in-flight Merge
	// so the recorded SHAs/path match what was just published.
	merge := *state.Merge
	merge.Status = conductor.MergeSucceeded
	merge.Reason = ReasonMergeOK
	state.Merge = &merge
	return finishSuccessfulMerge(in, state, now, &merge)
}

// finishSuccessfulMerge runs the post-publish lifecycle: source resync,
// merge-record save, cleanup matrix, cleanup save. Shared between the
// fresh-publish path and the recovery path that detects an already-
// published merge from a prior crashed run.
func finishSuccessfulMerge(in IntegrateInput, state *conductor.PlanState, now func() time.Time, merge *conductor.MergeOutcome) IntegrateResult {
	// H1: source checkout resync. update-ref advanced refs/heads/<target>
	// without touching the source worktree or index. When the target
	// branch is the source checkout's current HEAD, advance the source
	// to the new head so subsequent IsDirty preflights are honest. The
	// resync gate explicitly re-checks dirtiness so user edits made
	// during a long agent run cannot be silently discarded.
	syncStatus, syncErr := resyncSourceCheckout(in.Git, in.ControlRoot, merge.TargetRef, state.BaseHead, merge.PostMergeHead)
	merge.SourceSyncStatus = syncStatus
	if syncErr != nil {
		merge.SourceSyncError = syncErr.Error()
		progress(in.Progress, "merge %s: source resync failed — %v\n", in.PlanID, syncErr)
	}
	state.Merge = merge

	// H2: persist merge success BEFORE any destructive cleanup. If the
	// state save fails here, the merge has been published to the source
	// repo but the on-disk record would still say Pending. Running
	// cleanup would erase the only artifacts an operator could use to
	// reconstruct what happened — and a failed save here is detectable
	// by the next Integrate's recovery branch via PostMergeHead.
	if err := in.Project.SaveState(); err != nil {
		return IntegrateResult{
			PlanID:  in.PlanID,
			Merge:   merge,
			Cleanup: nil,
			Err:     fmt.Errorf("save merge state before cleanup: %w", err),
			Reason:  "merge-state-save-failed",
		}
	}

	cleanup := runCleanupMatrixWithPrior(in, state, merge.WorktreePath, state.Cleanup)
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
// ("failed", err) when the resync would have overwritten user edits or
// the reset itself failed.
//
// The dirty-source check uses IsDirtyAgainst(baseHead) rather than the
// generic IsDirty: by the time this runs, update-ref has already
// advanced refs/heads/<target> so HEAD points at the post-merge SHA
// while the worktree+index still reflect the pre-merge tree. A plain
// IsDirty would observe Springfield's own ref movement as "uncommitted
// changes" and refuse a reset that is supposed to make the checkout
// clean. Comparing against the recorded base_head reflects only the
// user's true edits — which is the data-loss invariant this gate
// protects.
func resyncSourceCheckout(g Git, controlRoot, targetRef, baseHead, newSHA string) (string, error) {
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
	if baseHead == "" {
		// Without a recorded base_head we cannot distinguish phantom
		// dirt from user dirt; refuse rather than risk silently
		// overwriting user edits.
		return "failed", fmt.Errorf("source resync: missing recorded base_head")
	}
	dirty, derr := g.IsDirtyAgainst(controlRoot, baseHead)
	if derr != nil {
		return "failed", fmt.Errorf("source dirty check before resync: %w", derr)
	}
	if dirty {
		return "failed", fmt.Errorf("source checkout has user changes since recorded base_head; refusing reset --hard to avoid silent data loss. Commit or stash, then run \"springfield start\" to retry source resync.")
	}
	if err := g.ResetHard(controlRoot, newSHA); err != nil {
		return "failed", err
	}
	return "synced", nil
}

// finalize assigns merge + preservation cleanup to state and persists it.
// Used on every non-success terminal path so partial state never leaks to
// disk after the merge phase has decided.
//
// When this attempt did NOT create a new merge worktree (e.g. drift
// refusal before WorktreeAddDetached), the prior merge worktree path —
// carried forward from a preceding refused/failed attempt — is retained
// so a repeated refusal does not erase the only artifact the first
// failure preserved for inspection.
func finalize(in IntegrateInput, state *conductor.PlanState, now func() time.Time, merge *conductor.MergeOutcome, reason string) IntegrateResult {
	if merge.WorktreePath == "" {
		merge.WorktreePath = priorMergeWorktreePath(state)
	}
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

// priorMergeWorktreePath returns the most recent recorded merge worktree
// path from prior state, preferring the cleanup ledger (which is the
// authoritative artifact record) over Merge.WorktreePath. Returns "" when
// no prior path was recorded.
func priorMergeWorktreePath(state *conductor.PlanState) string {
	if state.Cleanup != nil && state.Cleanup.MergeWorktree != nil && state.Cleanup.MergeWorktree.Path != "" {
		return state.Cleanup.MergeWorktree.Path
	}
	if state.Merge != nil && state.Merge.WorktreePath != "" {
		return state.Merge.WorktreePath
	}
	return ""
}

// refused builds a MergeOutcome with Status=Refused. Prior PostMergeHead
// and source-sync diagnostics are carried forward so a refusal never
// erases a SHA a previous attempt persisted — the on-disk record stays
// honest about what landed before the refusal.
func refused(state *conductor.PlanState, mergeWtPath, reason, msg string, when time.Time) *conductor.MergeOutcome {
	out := &conductor.MergeOutcome{
		Status:       conductor.MergeRefused,
		Mode:         string(ModeFFOnly),
		Reason:       reason,
		Error:        msg,
		TargetRef:    state.BaseRef,
		WorktreePath: mergeWtPath,
		AttemptedAt:  when,
	}
	carryForwardPriorMergeFields(state, out)
	return out
}

// failedMerge builds a MergeOutcome with Status=Failed. Prior diagnostics
// are carried forward (see refused).
func failedMerge(state *conductor.PlanState, targetHead, mergeWtPath, reason, msg string, when time.Time) *conductor.MergeOutcome {
	out := &conductor.MergeOutcome{
		Status:       conductor.MergeFailed,
		Mode:         string(ModeFFOnly),
		Reason:       reason,
		Error:        msg,
		TargetRef:    state.BaseRef,
		TargetHead:   targetHead,
		WorktreePath: mergeWtPath,
		AttemptedAt:  when,
	}
	carryForwardPriorMergeFields(state, out)
	return out
}

// carryForwardPriorMergeFields propagates SHA/diagnostic fields from a
// prior MergeOutcome into a new refusal/failure outcome. Without this
// step, a refusal that follows a prior attempt that already published
// (PostMergeHead set) would erase the post-merge SHA from the durable
// record, losing the trail an operator needs to understand what landed.
func carryForwardPriorMergeFields(state *conductor.PlanState, out *conductor.MergeOutcome) {
	if state == nil || state.Merge == nil {
		return
	}
	prior := state.Merge
	if out.PostMergeHead == "" && prior.PostMergeHead != "" {
		out.PostMergeHead = prior.PostMergeHead
	}
	if out.SourceSyncStatus == "" && prior.SourceSyncStatus != "" {
		out.SourceSyncStatus = prior.SourceSyncStatus
	}
	if out.SourceSyncError == "" && prior.SourceSyncError != "" {
		out.SourceSyncError = prior.SourceSyncError
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

// runCleanupMatrixWithPrior attempts to delete the merge worktree, the
// execution worktree, and the plan branch. Each artifact is tracked
// independently. Aggregate Status is "succeeded" when every artifact
// deleted cleanly, "failed" when any deletion errored.
//
// When prior is non-nil, any artifact whose prior status is
// CleanupSucceeded is carried forward without retrying — running
// `git worktree remove` against an already-removed path or
// `git branch -D` against an already-deleted branch fails, which would
// otherwise prevent a partially-clean re-entry from ever converging
// back to CleanupSucceeded once the originally-failing artifact is
// resolved. Pass nil for the fresh-cleanup case.
func runCleanupMatrixWithPrior(in IntegrateInput, state *conductor.PlanState, mergeWtPath string, prior *conductor.CleanupOutcome) *conductor.CleanupOutcome {
	var priorMW, priorXW, priorPB *conductor.ArtifactCleanup
	if prior != nil {
		priorMW = prior.MergeWorktree
		priorXW = prior.ExecutionWorktree
		priorPB = prior.PlanBranch
	}

	out := &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded}

	out.MergeWorktree = retryArtifactRemove(priorMW, mergeWtPath, "", func() error {
		return in.Git.WorktreeRemoveForce(in.ControlRoot, mergeWtPath)
	})
	if out.MergeWorktree.Status == conductor.CleanupFailed {
		out.Status = conductor.CleanupFailed
	}

	out.ExecutionWorktree = retryArtifactRemove(priorXW, state.WorktreePath, "", func() error {
		return in.Git.WorktreeRemoveForce(in.ControlRoot, state.WorktreePath)
	})
	if out.ExecutionWorktree.Status == conductor.CleanupFailed {
		out.Status = conductor.CleanupFailed
	}

	out.PlanBranch = retryArtifactRemove(priorPB, "", state.Branch, func() error {
		return in.Git.BranchDelete(in.ControlRoot, state.Branch)
	})
	if out.PlanBranch.Status == conductor.CleanupFailed {
		out.Status = conductor.CleanupFailed
	}

	return out
}

// retryArtifactRemove returns the new ArtifactCleanup record. When the
// prior record reports Succeeded, the deletion is NOT re-attempted —
// callers can safely re-enter cleanup without git tripping on an
// already-removed artifact.
func retryArtifactRemove(prior *conductor.ArtifactCleanup, path, branch string, attempt func() error) *conductor.ArtifactCleanup {
	if prior != nil && prior.Status == conductor.CleanupSucceeded {
		// Carry forward the prior success record verbatim so re-entry
		// converges to the same outcome.
		copy := *prior
		return &copy
	}
	out := &conductor.ArtifactCleanup{Status: conductor.CleanupSucceeded, Path: path, Branch: branch}
	if err := attempt(); err != nil {
		out.Status = conductor.CleanupFailed
		out.Error = err.Error()
	}
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
