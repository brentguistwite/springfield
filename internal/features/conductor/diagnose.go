package conductor

import (
	"fmt"
	"strings"
)

// PlanFailure describes one failed plan with its error and execution context.
type PlanFailure struct {
	Plan         string
	Error        string
	Agent        string
	EvidencePath string
	Attempts     int
}

// Diagnosis summarizes current conductor progress and next action.
type Diagnosis struct {
	Completed int
	Total     int
	Done      bool
	Failures  []PlanFailure
	// MergeIssues lists plans whose execution succeeded but whose merge
	// integration was refused or failed — surfaced separately so a
	// preserved merge worktree or refused publish doesn't hide behind a
	// "completed" execution status.
	MergeIssues []MergeIssue
	NextStep    string
}

// MergeIssue describes one plan whose merge integration is not in a clean
// success state. Cleanup failures appear here too so an operator sees the
// preserved artifacts that need attention even when the merge itself
// succeeded.
type MergeIssue struct {
	Plan         string
	MergeStatus  MergeStatus
	Reason       string
	Error        string
	TargetRef    string
	TargetHead   string
	BaseHead     string
	WorktreePath string
	Cleanup      *CleanupOutcome
}

// Diagnose inspects project state and returns internal guidance for conductor state.
//
// In parity 2 the surface always points at `springfield start`: that command
// is the user-facing execution entry point regardless of whether the project
// is using the plan-unit registry or the legacy sequential/batches projection.
func Diagnose(project *Project) *Diagnosis {
	schedule := BuildSchedule(project.Config)
	completed, total := schedule.Progress(project.State)

	failures := make([]PlanFailure, 0)
	mergeIssues := make([]MergeIssue, 0)
	for _, name := range project.AllPlans() {
		if project.PlanStatus(name) == StatusFailed {
			failures = append(failures, PlanFailure{
				Plan:         name,
				Error:        project.PlanError(name),
				Agent:        project.PlanAgent(name),
				EvidencePath: project.PlanEvidencePath(name),
				Attempts:     project.PlanAttempts(name),
			})
		}
		ps, ok := project.State.Plans[name]
		if !ok || ps == nil || ps.Merge == nil {
			continue
		}
		// MergePending means execution finished but planmerge.Integrate
		// has not yet run. The next `springfield start` will pick the
		// plan up — surfacing it as a "merge issue" would mislead the
		// operator that something needs fixing.
		if ps.Merge.Status == MergePending {
			continue
		}
		if ps.Merge.Status == MergeSucceeded && (ps.Cleanup == nil || ps.Cleanup.Status != CleanupFailed) {
			continue
		}
		mergeIssues = append(mergeIssues, MergeIssue{
			Plan:         name,
			MergeStatus:  ps.Merge.Status,
			Reason:       ps.Merge.Reason,
			Error:        ps.Merge.Error,
			TargetRef:    ps.Merge.TargetRef,
			TargetHead:   ps.Merge.TargetHead,
			BaseHead:     ps.BaseHead,
			WorktreePath: ps.Merge.WorktreePath,
			Cleanup:      ps.Cleanup,
		})
	}

	done := schedule.IsComplete(project.State)
	nextStep := "Run: springfield start"
	switch {
	case total == 0:
		nextStep = "No plans configured. Run \"springfield plans add\" to register one."
	case done:
		nextStep = "All plans completed successfully."
	case len(failures) > 0:
		nextStep = "Inspect failures (see status), fix the underlying cause, then re-run: springfield start"
	}
	if len(mergeIssues) > 0 {
		nextStep = "Resolve merge integration issues (see status), then re-run: springfield start"
	}

	return &Diagnosis{
		Completed:   completed,
		Total:       total,
		Done:        done,
		Failures:    failures,
		MergeIssues: mergeIssues,
		NextStep:    nextStep,
	}
}

// Report renders a readable diagnosis summary.
func (d *Diagnosis) Report() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Progress: %d/%d plans completed\n", d.Completed, d.Total)

	if d.Total == 0 {
		fmt.Fprintf(&builder, "\nNext step: %s\n", d.NextStep)
		return builder.String()
	}

	if d.Done && len(d.MergeIssues) == 0 {
		builder.WriteString("Status: all plans completed\n")
		return builder.String()
	}

	if len(d.Failures) > 0 {
		fmt.Fprintf(&builder, "\nFailed plans (%d):\n", len(d.Failures))
		for _, f := range d.Failures {
			fmt.Fprintf(&builder, "  - %s: %s\n", f.Plan, f.Error)
			if f.Agent != "" {
				fmt.Fprintf(&builder, "    Agent: %s\n", f.Agent)
			}
			if f.EvidencePath != "" {
				fmt.Fprintf(&builder, "    Evidence: %s\n", f.EvidencePath)
			}
			if f.Attempts > 1 {
				fmt.Fprintf(&builder, "    Attempts: %d\n", f.Attempts)
			}
		}
	}

	if len(d.MergeIssues) > 0 {
		fmt.Fprintf(&builder, "\nMerge issues (%d):\n", len(d.MergeIssues))
		for _, m := range d.MergeIssues {
			fmt.Fprintf(&builder, "  - %s: merge %s (%s)\n", m.Plan, m.MergeStatus, m.Reason)
			if m.Error != "" {
				fmt.Fprintf(&builder, "    detail: %s\n", m.Error)
			}
			if m.TargetRef != "" {
				fmt.Fprintf(&builder, "    target: %s; recorded base %s; observed %s\n",
					m.TargetRef, m.BaseHead, m.TargetHead)
			}
			if m.WorktreePath != "" {
				fmt.Fprintf(&builder, "    merge worktree (preserved): %s\n", m.WorktreePath)
			}
			if m.Cleanup != nil && m.Cleanup.Status == CleanupFailed {
				fmt.Fprintf(&builder, "    cleanup failed; preserved artifacts remain on disk\n")
			}
		}
	}

	fmt.Fprintf(&builder, "\nNext step: %s\n", d.NextStep)
	return builder.String()
}
