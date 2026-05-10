package conductor

import (
	"fmt"
	"time"
)

// RecoverRetry resets a failed or interrupted plan to pending for re-execution.
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
	if ps.Status != StatusFailed && ps.Status != StatusInterrupted {
		return nil, fmt.Errorf("retry requires failed or interrupted status (plan %q is %s)", planID, ps.Status)
	}

	// Recovery resets failed/interrupted to pending so the next start can
	// dispatch. The lifecycle UI shows this as a direct return to the active
	// path; the intermediate `pending` is internal bookkeeping.
	// lifecycle:edge from=failed to=running kind=recovery label="retry"
	// lifecycle:edge from=interrupted to=running kind=recovery label="retry"
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
