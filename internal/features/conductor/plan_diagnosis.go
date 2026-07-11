package conductor

import (
	"fmt"
	"strings"

	"springfield/internal/features/prd"
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

	// Stories is populated only when a PRD is supplied to DiagnosePlanWithPRD.
	// Nil when no PRD was provided (backward-compat).
	Stories *StoryDiagnosis
}

// StoryDiagnosis holds per-story status for the plan's PRD, computed at
// diagnosis time from the current passes field in prd.json.
type StoryDiagnosis struct {
	Entries       []StoryEntry
	CurrentTarget string // ID of the story NextStory would return; empty when all done
}

// StoryEntry is one user story in the diagnosis output.
type StoryEntry struct {
	ID     string
	Title  string
	Passes bool
}

// DiagnosePlanWithPRD builds a full plan diagnosis and appends per-story status
// from p. p may be nil, in which case behavior is identical to DiagnosePlan.
// The current target story is tagged via NextStory so the operator knows which
// story the next run would attempt.
func DiagnosePlanWithPRD(project *Project, planID string, wt *WorktreeInspection, p *prd.PRD) *PlanDiagnosis {
	d := DiagnosePlan(project, planID, wt)
	if p == nil {
		return d
	}
	sd := &StoryDiagnosis{}
	for _, s := range p.UserStories {
		sd.Entries = append(sd.Entries, StoryEntry{
			ID:     s.ID,
			Title:  s.Title,
			Passes: s.Passes,
		})
	}
	// Tag the current target so the operator sees what the next iteration would attempt.
	next, ok := nextStoryID(*p)
	if ok {
		sd.CurrentTarget = next
	}
	d.Stories = sd
	return d
}

// nextStoryID returns the ID of the next eligible story. The eligibility rule is
// single-sourced in prd.NextEligibleStory (shared with planrun.NextStory and the
// status projection) so this diagnosis view cannot disagree with the runner about
// which story is current.
func nextStoryID(p prd.PRD) (string, bool) {
	s, ok := prd.NextEligibleStory(p)
	if !ok {
		return "", false
	}
	return s.ID, true
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

	case StatusNeedsHuman:
		// The remediation differs by which completion gate halted the plan: the
		// objective verify gate points the operator at the failing command, while
		// the subjective review gate points at the reviewer's findings. Both re-run
		// the same "retry" action (status-keyed reset to pending); only the guidance
		// changes so a plan that failed `go test` is not told to "address the
		// reviewer's findings."
		desc := "Re-review: address the reviewer's findings (edit in the preserved worktree, or revise the plan), then reset to pending and re-run on next \"springfield start\" — the plan re-enters the pre-merge review gate (it does NOT skip review)."
		if ps.ExitReason == "verify-needs-human" {
			desc = "Re-verify: fix the failing verify command (edit in the preserved worktree, or revise the plan), then reset to pending and re-run on next \"springfield start\" — the plan re-enters the verify gate (it does NOT skip verification)."
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

	if d.Stories != nil && len(d.Stories.Entries) > 0 {
		b.WriteString("\nStories:\n")
		for _, e := range d.Stories.Entries {
			status := "pending"
			if e.Passes {
				status = "passed"
			}
			suffix := ""
			if e.ID == d.Stories.CurrentTarget {
				suffix = "  (current target)"
			}
			fmt.Fprintf(&b, "  %s  %s  %s%s\n", e.ID, e.Title, status, suffix)
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
