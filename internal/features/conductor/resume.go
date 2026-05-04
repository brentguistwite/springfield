package conductor

import (
	"fmt"
	"sort"
	"time"
)

// NormalizeStaleRunning rewrites any persisted running plan into the minimal
// honest parity-4 interruption model. A plan is considered stale as soon as
// the product boundary (`springfield start` / `springfield status`) observes
// it with no active batch runtime owning execution.
func (p *Project) NormalizeStaleRunning(at time.Time) []string {
	if p == nil || p.State == nil {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}

	changed := make([]string, 0)
	for name, plan := range p.State.Plans {
		if plan == nil || plan.Status != StatusRunning {
			continue
		}
		plan.Status = StatusInterrupted
		plan.ExitReason = ExitInterruptedProcessExit
		plan.EndedAt = at
		if plan.Error == "" {
			plan.Error = fmt.Sprintf(
				"previous Springfield run exited before plan %q recorded a terminal result; run \"springfield start\" to resume",
				name,
			)
		}
		changed = append(changed, name)
	}
	sort.Strings(changed)
	return changed
}

func nextPlannedAction(project *Project) string {
	if project == nil || project.Config == nil {
		return nextStepRunStart
	}

	next := BuildSchedule(project.Config).NextPlans(project.State)
	if len(next) == 0 {
		return "All registered plans completed."
	}

	planID := next[0]
	plan := project.State.Plans[planID]
	if plan == nil {
		return nextStepRunStart
	}

	switch plan.Status {
	case StatusInterrupted:
		return fmt.Sprintf("Run \"springfield start\" to resume interrupted plan %q from its recorded worktree.", planID)
	case StatusFailed:
		return fmt.Sprintf("Inspect plan %q failure above, fix the underlying cause if needed, then re-run: springfield start", planID)
	case StatusCompleted:
		if plan.Merge != nil && plan.Merge.Status == MergePending {
			return fmt.Sprintf("Run \"springfield start\" to continue merge integration for completed plan %q.", planID)
		}
		if !plan.IsIntegrated() {
			return fmt.Sprintf("Resolve merge integration issues for plan %q (see status), then re-run: springfield start", planID)
		}
	}

	return nextStepRunStart
}
