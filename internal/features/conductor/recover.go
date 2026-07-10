package conductor

import (
	"fmt"
	"strings"
	"time"

	"springfield/internal/features/prd"
)

// RecoverRetry resets a failed, interrupted, or needs-human plan to pending for
// re-execution. A needs-human plan re-enters the completion gates on re-run
// (verify then review — its stories stay passed, so the runner hits the
// top-of-loop completion path).
// The recovery action is appended to the plan's history; the caller must persist
// via SaveState.
//
// prd.json preservation invariant: RecoverRetry intentionally does NOT touch
// prd.json or progress.md. The durable per-story passes state in prd.json is
// the source of truth across retries. On re-entry, the runner's NextStory
// automatically skips already-passed stories, so work completed before the
// failure is not repeated. Any code path that modifies prd.json during recovery
// would destroy this invariant and force the runner to re-execute completed work.
func (p *Project) RecoverRetry(planID string) (*RecoveryAction, error) {
	ps, ok := p.State.Plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan %q has no recorded state", planID)
	}
	if ps.Status != StatusFailed && ps.Status != StatusInterrupted && ps.Status != StatusNeedsHuman {
		return nil, fmt.Errorf("retry requires failed, interrupted, or needs-human status (plan %q is %s)", planID, ps.Status)
	}

	rec := RecoveryAction{
		Action: "retry",
		Reason: fmt.Sprintf("reset from %s to pending for re-execution", ps.Status),
		At:     time.Now(),
	}

	ps.Status = StatusPending
	ps.Error = ""
	ps.ExitReason = ""
	ps.Merge = nil
	ps.Cleanup = nil
	ps.RecoveryHistory = append(ps.RecoveryHistory, rec)
	return &rec, nil
}

// ResetPlanFresh discards a prior attempt entirely: it resets the plan to a
// clean first-run state, clearing the recorded worktree path, branch, base
// head, and input digest. Unlike RecoverRetry (which resumes the preserved
// worktree) and AcceptInputDrift (which keeps the worktree and only re-records
// the digest), reset is the discard path — the caller is expected to also
// remove the on-disk worktree and delete the plan branch so the next start
// re-creates them from base. Refuses a mid-flight running plan and a
// fully-integrated completed plan (its work is already merged). The caller
// must persist via SaveState.
func (p *Project) ResetPlanFresh(planID string) (*RecoveryAction, error) {
	ps, ok := p.State.Plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan %q has no recorded state", planID)
	}
	if ps.Status == StatusRunning {
		return nil, fmt.Errorf("plan %q is currently running; wait for it to finish or run \"springfield recover --plan %s\" to normalize state before resetting", planID, planID)
	}
	if ps.Status == StatusCompleted && ps.IsIntegrated() {
		return nil, fmt.Errorf("plan %q is already integrated; its work is merged — remove the plan unit instead of resetting", planID)
	}

	rec := RecoveryAction{
		Action: "reset",
		Reason: fmt.Sprintf("discarded prior attempt and reset from %s to a clean first-run", ps.Status),
		At:     time.Now(),
	}

	ps.Status = StatusPending
	ps.Error = ""
	ps.ExitReason = ""
	ps.Merge = nil
	ps.Cleanup = nil
	ps.Attempts = 0
	ps.WorktreePath = ""
	ps.Branch = ""
	ps.BaseRef = ""
	ps.BaseHead = ""
	ps.InputDigest = ""
	// Clear the prior run-record too: PlanHead points into the now-deleted
	// branch and would surface as a dangling SHA in status/diagnosis; the rest
	// describe an attempt that no longer exists after a clean reset.
	ps.PlanHead = ""
	ps.EvidencePath = ""
	ps.Agent = ""
	ps.StartedAt = time.Time{}
	ps.EndedAt = time.Time{}
	ps.RecoveryHistory = append(ps.RecoveryHistory, rec)
	return &rec, nil
}

// RecoverRetryMerge clears merge and cleanup state for a completed plan whose
// merge was refused or failed before the target branch was advanced. The next
// springfield start re-enters the full merge integration phase. The caller must
// persist via SaveState.
//
// Plans whose merge already succeeded must use RecoverRetryIntegration instead;
// clearing a succeeded merge record would destroy metadata the re-entry path
// depends on (PostMergeHead, TargetRef, WorktreePath).
func (p *Project) RecoverRetryMerge(planID string) (*RecoveryAction, error) {
	ps, ok := p.State.Plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan %q has no recorded state", planID)
	}
	if ps.Status != StatusCompleted {
		return nil, fmt.Errorf("retry-merge requires completed status (plan %q is %s)", planID, ps.Status)
	}
	if ps.Merge == nil {
		return nil, fmt.Errorf("plan %q has no merge state to retry", planID)
	}
	if ps.Merge.Status == MergePending {
		return nil, fmt.Errorf("plan %q has a pending merge — run \"springfield start\" to continue", planID)
	}
	if ps.Merge.Status == MergeSucceeded {
		return nil, fmt.Errorf("plan %q merge already succeeded — use retry-integration to re-attempt cleanup/sync", planID)
	}
	if ps.IsIntegrated() {
		return nil, fmt.Errorf("plan %q is already fully integrated", planID)
	}

	rec := RecoveryAction{
		Action: "retry-merge",
		Reason: fmt.Sprintf("cleared merge state (was %s) for re-integration", ps.Merge.Status),
		At:     time.Now(),
	}

	ps.Merge = nil
	ps.Cleanup = nil
	ps.RecoveryHistory = append(ps.RecoveryHistory, rec)
	return &rec, nil
}

// MarkPlanCompleted is the operator escape hatch (A9) for a plan whose work
// finished in the worktree but which Springfield recorded as failed,
// interrupted, or needs-human. It validates that every story in stories has
// Passes=true — rejecting with an error that names the unpassed stories
// otherwise — then flips the plan to StatusCompleted and queues a pending
// merge. The caller must persist via SaveState.
//
// Merge is set to MergePending, not MergeSucceeded: mark-completed never
// publishes a merge itself. The next springfield start re-enters the normal
// merge integration phase, which still runs the target-drift check and the
// ff-only publish against the recorded base_head. An already-completed plan is
// rejected; its merge is re-driven via RecoverRetryMerge/RecoverRetryIntegration.
//
// prd.json is the source of truth for per-story passes state; the caller reads
// it and passes the stories in so this method stays free of file IO.
func (p *Project) MarkPlanCompleted(planID string, stories []prd.UserStory) (*RecoveryAction, error) {
	ps, ok := p.State.Plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan %q has no recorded state", planID)
	}
	if ps.Status == StatusCompleted {
		return nil, fmt.Errorf("plan %q is already completed; use retry-merge or retry-integration to re-drive its merge", planID)
	}
	// A running plan is mid-flight; the active runner will overwrite this
	// status flip with its own MarkPassed/MarkFailed call when it finishes,
	// silently discarding the operator's intent. Refuse and tell the operator
	// to wait or recover the run first — same shape as plan --replace's guard.
	if ps.Status == StatusRunning {
		return nil, fmt.Errorf("plan %q is currently running (status=running); wait for it to finish or run \"springfield recover --plan %s\" to normalize state before marking completed", planID, planID)
	}
	if len(stories) == 0 {
		return nil, fmt.Errorf("plan %q has no stories in prd.json; refusing to mark completed", planID)
	}

	var unpassed []string
	for _, s := range stories {
		if !s.Passes {
			unpassed = append(unpassed, s.ID)
		}
	}
	if len(unpassed) > 0 {
		return nil, fmt.Errorf("cannot mark plan %q completed: %d of %d stories not passing: %s",
			planID, len(unpassed), len(stories), strings.Join(unpassed, ", "))
	}

	now := time.Now()
	rec := RecoveryAction{
		Action: "mark-completed",
		Reason: fmt.Sprintf("operator marked completed from %s; queued pending merge", ps.Status),
		At:     now,
	}

	ps.Status = StatusCompleted
	ps.Error = ""
	ps.ExitReason = ""
	ps.Merge = &MergeOutcome{Status: MergePending, AttemptedAt: now}
	ps.Cleanup = nil
	ps.RecoveryHistory = append(ps.RecoveryHistory, rec)
	return &rec, nil
}

// AcceptInputDrift is the operator escape hatch (A10) for a deliberate input
// change that the digest correctly flagged as drift (e.g. an updated AGENTS.md
// or an intentional prd.json edit). It records digest as the plan's new
// InputDigest and resets the plan to pending — the same field reset as
// RecoverRetry — so the next springfield start resumes (or re-runs) against the
// changed inputs instead of refusing with preflight-input-drift. The caller
// must persist via SaveState.
//
// The digest is passed in rather than computed here: the canonical InputDigest
// lives in planrun, which imports execution, so neither conductor nor execution
// can compute it without an import cycle. Taking it as an argument also keeps
// this method free of file IO, mirroring MarkPlanCompleted.
//
// A completed plan is rejected: accept-drift resets a non-completed plan for
// re-run, while a finished plan's merge is re-driven via RecoverRetryMerge or
// RecoverRetryIntegration.
func (p *Project) AcceptInputDrift(planID, digest string) (*RecoveryAction, error) {
	ps, ok := p.State.Plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan %q has no recorded state", planID)
	}
	if ps.Status == StatusCompleted {
		return nil, fmt.Errorf("plan %q is already completed; accept-drift resets a non-completed plan for re-run with changed inputs", planID)
	}
	// A running plan is mid-flight; the active runner committed to the
	// preflight-time digest, and rewriting InputDigest now would leave it
	// inconsistent with what the runner is actually executing against. Refuse.
	if ps.Status == StatusRunning {
		return nil, fmt.Errorf("plan %q is currently running (status=running); wait for it to finish or run \"springfield recover --plan %s\" to normalize state before accepting drift", planID, planID)
	}
	// Idempotence + misuse guard: if the recorded digest already matches the
	// supplied digest, there is NO DRIFT — regardless of plan status. The
	// pending case is a true no-op; the failed/interrupted/needs-human case
	// with matching digest is operator misuse (they want RecoverRetry, not
	// accept-drift, because the failure wasn't from changed inputs). Both
	// should reject with a clear pointer to the right path, rather than
	// silently rewriting the digest to the same value and appending a
	// misleading "accepted drift" history entry. Adversarial review round 2
	// (R3F3) caught the broader case; the original guard only covered pending.
	if ps.InputDigest == digest {
		if ps.Status == StatusPending {
			return nil, fmt.Errorf("plan %q is already pending with the supplied digest; nothing to accept", planID)
		}
		return nil, fmt.Errorf("plan %q already has the supplied digest recorded; the failure was not caused by input drift — use \"springfield recover --plan %s\" to retry instead", planID, planID)
	}

	rec := RecoveryAction{
		Action: "accept-drift",
		Reason: fmt.Sprintf("recorded new input digest and reset from %s to pending", ps.Status),
		At:     time.Now(),
	}

	ps.Status = StatusPending
	ps.Error = ""
	ps.ExitReason = ""
	ps.Merge = nil
	ps.Cleanup = nil
	ps.InputDigest = digest
	ps.RecoveryHistory = append(ps.RecoveryHistory, rec)
	return &rec, nil
}

// RecoverRetryIntegration clears post-merge state (cleanup, source-sync) for a
// completed plan whose merge succeeded but post-publish integration did not
// complete. Preserves the merge record so planmerge.Integrate follows the
// MergeSucceeded re-entry path. The caller must persist via SaveState.
func (p *Project) RecoverRetryIntegration(planID string) (*RecoveryAction, error) {
	ps, ok := p.State.Plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan %q has no recorded state", planID)
	}
	if ps.Status != StatusCompleted {
		return nil, fmt.Errorf("retry-integration requires completed status (plan %q is %s)", planID, ps.Status)
	}
	if ps.Merge == nil {
		return nil, fmt.Errorf("plan %q has no merge state", planID)
	}
	if ps.Merge.Status != MergeSucceeded {
		return nil, fmt.Errorf("retry-integration requires merge-succeeded (plan %q merge is %s)", planID, ps.Merge.Status)
	}
	if ps.IsIntegrated() {
		return nil, fmt.Errorf("plan %q is already fully integrated", planID)
	}

	rec := RecoveryAction{
		Action: "retry-integration",
		Reason: fmt.Sprintf("cleared cleanup/sync state for re-integration (merge preserved at %s)", ps.Merge.PostMergeHead),
		At:     time.Now(),
	}

	ps.Merge.SourceSyncStatus = ""
	ps.Merge.SourceSyncError = ""
	ps.Cleanup = nil
	ps.RecoveryHistory = append(ps.RecoveryHistory, rec)
	return &rec, nil
}
