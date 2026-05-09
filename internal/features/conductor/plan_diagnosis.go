package conductor

import (
	"fmt"
	"strings"
)

// WorktreeInspection captures live git state for a plan's worktree, computed by
// the caller (execution layer) and passed into DiagnosePlan so the conductor
// package stays free of git I/O.
type WorktreeInspection struct {
	Exists        bool   `json:"exists"`
	Registered    bool   `json:"registered"`
	IsDirty       bool   `json:"is_dirty"`
	BranchHead    string `json:"branch_head,omitempty"`
	HasNewCommits bool   `json:"has_new_commits"`
}

// RecoveryOption describes one available recovery action for a diagnosed plan.
type RecoveryOption struct {
	Action      string
	Description string
}

// PlanDiagnosis is the full diagnostic picture for one plan, combining persisted
// state with optional live worktree inspection and available recovery actions.
type PlanDiagnosis struct {
	PlanID       string
	Status       PlanStatus
	Error        string
	Agent        string
	EvidencePath string
	Attempts     int
	ExitReason   string

	WorktreePath string
	Branch       string
	BaseRef      string
	BaseHead     string
	PlanHead     string

	Merge   *MergeOutcome
	Cleanup *CleanupOutcome

	RecoveryHistory  []RecoveryAction
	Worktree         *WorktreeInspection
	AvailableActions []RecoveryOption
}

// DiagnosePlan builds a diagnosis for one plan. The optional worktree inspection
// enriches the diagnosis with live git state; pass nil when the worktree path is
// unknown or the caller cannot reach git.
func DiagnosePlan(project *Project, planID string, wt *WorktreeInspection) *PlanDiagnosis {
	ps, ok := project.State.Plans[planID]
	if !ok {
		return &PlanDiagnosis{
			PlanID: planID,
			Status: StatusPending,
		}
	}

	d := &PlanDiagnosis{
		PlanID:          planID,
		Status:          ps.Status,
		Error:           ps.Error,
		Agent:           ps.Agent,
		EvidencePath:    ps.EvidencePath,
		Attempts:        ps.Attempts,
		ExitReason:      ps.ExitReason,
		WorktreePath:    ps.WorktreePath,
		Branch:          ps.Branch,
		BaseRef:         ps.BaseRef,
		BaseHead:        ps.BaseHead,
		PlanHead:        ps.PlanHead,
		Merge:           ps.Merge,
		Cleanup:         ps.Cleanup,
		RecoveryHistory: ps.RecoveryHistory,
		Worktree:        wt,
	}

	d.AvailableActions = availableActions(ps, wt)
	return d
}

func availableActions(ps *PlanState, wt *WorktreeInspection) []RecoveryOption {
	var actions []RecoveryOption

	switch ps.Status {
	case StatusFailed:
		desc := "Reset to pending and re-execute on next \"springfield start\""
		if wt != nil && wt.HasNewCommits {
			desc += " (note: worktree has commits beyond base — work may have partially completed)"
		}
		actions = append(actions, RecoveryOption{Action: "retry", Description: desc})

	case StatusInterrupted:
		desc := "Reset to pending and resume on next \"springfield start\""
		if wt != nil && wt.IsDirty {
			desc += " (worktree has uncommitted changes that will be visible to the resumed agent)"
		}
		actions = append(actions, RecoveryOption{Action: "retry", Description: desc})

	case StatusCompleted:
		if ps.Merge == nil {
			// Completed with no merge record — legacy flow or save failure
			// between MarkCompleted and MergePending. IsIntegrated returns
			// true for nil-Merge, so no recovery needed here.
			break
		}
		if !ps.IsIntegrated() {
			switch {
			case ps.Merge.Status == MergeRefused:
				actions = append(actions, RecoveryOption{
					Action:      "retry-merge",
					Description: "Clear merge state and re-attempt on next \"springfield start\" (ensure target branch is at expected state)",
				})
			case ps.Merge.Status == MergeFailed:
				actions = append(actions, RecoveryOption{
					Action:      "retry-merge",
					Description: "Clear merge state and re-attempt on next \"springfield start\"",
				})
			case ps.Merge.Status == MergeSucceeded && ps.Cleanup != nil && ps.Cleanup.Status == CleanupFailed:
				actions = append(actions, RecoveryOption{
					Action:      "retry-integration",
					Description: "Re-attempt cleanup (merge already published; merge state preserved)",
				})
			case ps.Merge.Status == MergeSucceeded && ps.Merge.SourceSyncStatus == "failed":
				actions = append(actions, RecoveryOption{
					Action:      "retry-integration",
					Description: "Re-attempt source sync and cleanup (merge already published; merge state preserved)",
				})
			case ps.Merge.Status == MergePending:
				// MergePending is not an error — next start picks it up.
			default:
				if ps.Merge.Status == MergeSucceeded {
					// MergeSucceeded + Cleanup nil (save failure between merge and cleanup recording).
					actions = append(actions, RecoveryOption{
						Action:      "retry-integration",
						Description: "Re-attempt cleanup/sync (merge already published; merge state preserved)",
					})
				}
			}
		}
	}

	return actions
}

// Render produces a human-readable diagnosis report.
func (d *PlanDiagnosis) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: %s\n", d.PlanID)
	fmt.Fprintf(&b, "Status: %s\n", d.Status)
	if d.Error != "" {
		fmt.Fprintf(&b, "Error: %s\n", d.Error)
	}
	if d.Agent != "" {
		fmt.Fprintf(&b, "Agent: %s\n", d.Agent)
	}
	if d.EvidencePath != "" {
		fmt.Fprintf(&b, "Evidence: %s\n", d.EvidencePath)
	}
	if d.Attempts > 0 {
		fmt.Fprintf(&b, "Attempts: %d\n", d.Attempts)
	}
	if d.ExitReason != "" {
		fmt.Fprintf(&b, "Exit reason: %s\n", d.ExitReason)
	}
	if d.WorktreePath != "" {
		fmt.Fprintf(&b, "Worktree: %s\n", d.WorktreePath)
	}
	if d.Branch != "" {
		fmt.Fprintf(&b, "Branch: %s", d.Branch)
		if d.BaseRef != "" {
			fmt.Fprintf(&b, " (base %s @ %s)", d.BaseRef, shortSHA(d.BaseHead))
		}
		b.WriteByte('\n')
	}
	if d.PlanHead != "" {
		fmt.Fprintf(&b, "Plan head: %s\n", shortSHA(d.PlanHead))
	}

	if d.Worktree != nil {
		b.WriteString("\nWorktree inspection:\n")
		fmt.Fprintf(&b, "  exists: %s\n", boolYesNo(d.Worktree.Exists))
		if d.Worktree.Exists {
			fmt.Fprintf(&b, "  registered: %s\n", boolYesNo(d.Worktree.Registered))
			fmt.Fprintf(&b, "  dirty: %s\n", boolYesNo(d.Worktree.IsDirty))
			if d.Worktree.BranchHead != "" {
				fmt.Fprintf(&b, "  branch head: %s\n", shortSHA(d.Worktree.BranchHead))
			}
			fmt.Fprintf(&b, "  commits beyond base: %s\n", boolYesNo(d.Worktree.HasNewCommits))
		}
	}

	renderMerge(&b, d.Merge)
	renderCleanup(&b, d.Cleanup)

	if len(d.RecoveryHistory) > 0 {
		b.WriteString("\nRecovery history:\n")
		for _, r := range d.RecoveryHistory {
			fmt.Fprintf(&b, "  - %s: %s (%s)\n", r.At.Format("2006-01-02T15:04:05Z07:00"), r.Action, r.Reason)
		}
	}

	if len(d.AvailableActions) > 0 {
		b.WriteString("\nAvailable actions:\n")
		for _, a := range d.AvailableActions {
			fmt.Fprintf(&b, "  %s — %s\n", a.Action, a.Description)
		}
		fmt.Fprintf(&b, "\nTo recover: springfield recover --plan %s\n", d.PlanID)
	} else if d.Status == StatusPending || d.Status == StatusRunning {
		fmt.Fprintf(&b, "\nNo recovery needed — plan is %s.\n", d.Status)
	} else if d.Status == StatusCompleted && (d.Merge == nil || d.Merge.Status == MergePending) {
		b.WriteString("\nNo recovery needed — run \"springfield start\" to continue.\n")
	} else if d.Status == StatusCompleted {
		b.WriteString("\nPlan fully integrated — no action needed.\n")
	}

	return b.String()
}

func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
