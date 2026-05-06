package conductor

import (
	"fmt"
	"strings"
)

// RegistryStatus is the rendered plan-registry surface for `springfield status`
// when no batch runtime state exists.
type RegistryStatus struct {
	HasConfig bool
	Units     []PlanUnitStatus
	Completed int
	Total     int
	Failures  []PlanFailure
	NextStep  string
	Queue     *QueueState
}

// PlanUnitStatus is one ordered plan unit annotated with its current state.
type PlanUnitStatus struct {
	Unit         PlanUnit
	Status       PlanStatus
	Error        string
	Agent        string
	EvidencePath string
	Attempts     int
	WorktreePath string
	Branch       string
	BaseRef      string
	BaseHead     string
	PlanHead     string
	ExitReason   string
	Merge        *MergeOutcome
	Cleanup      *CleanupOutcome
}

// nextStepRunStart is the parity-2 message: a populated registry can be
// executed one plan at a time via `springfield start`.
const nextStepRunStart = "Run \"springfield start\" to execute the next registered plan in its own git worktree."

// BuildRegistryStatus pairs each ordered plan unit with its mutable state and
// computes overall counts plus the suggested next action.
func BuildRegistryStatus(project *Project) *RegistryStatus {
	if project == nil || project.Config == nil {
		return &RegistryStatus{NextStep: "No Springfield execution config. Run \"springfield init\", then \"springfield plans add\" to register a plan."}
	}

	rs := &RegistryStatus{HasConfig: true}

	if len(project.Config.PlanUnits) == 0 {
		rs.NextStep = "No plans configured. Run \"springfield plans add\" to register one."
		return rs
	}

	ordered := make([]PlanUnit, len(project.Config.PlanUnits))
	copy(ordered, project.Config.PlanUnits)
	idsInOrder := OrderedPlanUnitIDs(ordered)
	byID := make(map[string]PlanUnit, len(ordered))
	for _, u := range ordered {
		byID[u.ID] = u
	}

	var failures []PlanFailure
	for _, id := range idsInOrder {
		u := byID[id]
		st := project.PlanStatus(id)
		entry := PlanUnitStatus{
			Unit:         u,
			Status:       st,
			Error:        project.PlanError(id),
			Agent:        project.PlanAgent(id),
			EvidencePath: project.PlanEvidencePath(id),
			Attempts:     project.PlanAttempts(id),
		}
		if ps, ok := project.State.Plans[id]; ok && ps != nil {
			entry.WorktreePath = ps.WorktreePath
			entry.Branch = ps.Branch
			entry.BaseRef = ps.BaseRef
			entry.BaseHead = ps.BaseHead
			entry.PlanHead = ps.PlanHead
			entry.ExitReason = ps.ExitReason
			entry.Merge = ps.Merge
			entry.Cleanup = ps.Cleanup
		}
		rs.Units = append(rs.Units, entry)
		rs.Total++
		// Completed counter reports queue-integrated plans only: a plan
		// whose execution succeeded but whose merge was refused/failed or
		// whose cleanup failed must not advance the counter, otherwise
		// status would emit "all plans completed" alongside merge
		// diagnostics that say otherwise.
		if ps, ok := project.State.Plans[id]; ok && ps != nil && ps.IsIntegrated() {
			rs.Completed++
		}
		if st == StatusFailed {
			failures = append(failures, PlanFailure{
				Plan:         id,
				Error:        entry.Error,
				Agent:        entry.Agent,
				EvidencePath: entry.EvidencePath,
				Attempts:     entry.Attempts,
			})
		}
	}
	rs.Failures = failures

	rs.Queue = project.State.Queue

	switch {
	case rs.Completed == rs.Total && rs.Total > 0:
		rs.NextStep = "All registered plans completed."
	default:
		rs.NextStep = nextPlannedAction(project)
	}
	return rs
}

func renderMerge(b *strings.Builder, m *MergeOutcome) {
	if m == nil {
		return
	}
	if m.Reason == "" {
		fmt.Fprintf(b, "     merge: %s\n", m.Status)
	} else {
		fmt.Fprintf(b, "     merge: %s (%s)\n", m.Status, m.Reason)
	}
	if m.TargetRef != "" {
		fmt.Fprintf(b, "       target: %s @ %s\n", m.TargetRef, shortSHA(m.TargetHead))
	}
	if m.PostMergeHead != "" {
		fmt.Fprintf(b, "       post-merge head: %s\n", shortSHA(m.PostMergeHead))
	}
	if m.WorktreePath != "" && m.Status != MergeSucceeded {
		fmt.Fprintf(b, "       merge worktree (preserved): %s\n", m.WorktreePath)
	}
	if m.Error != "" {
		fmt.Fprintf(b, "       detail: %s\n", m.Error)
	}
}

func renderCleanup(b *strings.Builder, c *CleanupOutcome) {
	if c == nil {
		return
	}
	if c.Status == CleanupSucceeded {
		// No need to surface the per-artifact deleted list when everything
		// is clean — keep status output focused on what is still on disk.
		return
	}
	fmt.Fprintf(b, "     cleanup: %s\n", c.Status)
	for _, p := range cleanupArtifactPairs(c) {
		art := p.artifact
		if art == nil {
			continue
		}
		switch art.Status {
		case CleanupPreserved:
			fmt.Fprintf(b, "       %s preserved: %s\n", p.label, displayArtifactRef(art))
		case CleanupFailed:
			fmt.Fprintf(b, "       %s cleanup failed: %s (preserved at %s)\n", p.label, art.Error, displayArtifactRef(art))
		}
	}
}

// cleanupArtifactPair pairs a label with one cleanup artifact in the
// canonical render order so status / diagnose / start.go produce stable,
// run-to-run output. A map iteration would randomize order in Go.
type cleanupArtifactPair struct {
	label    string
	artifact *ArtifactCleanup
}

func cleanupArtifactPairs(c *CleanupOutcome) []cleanupArtifactPair {
	return []cleanupArtifactPair{
		{"merge worktree", c.MergeWorktree},
		{"execution worktree", c.ExecutionWorktree},
		{"plan branch", c.PlanBranch},
	}
}

func displayArtifactRef(art *ArtifactCleanup) string {
	if art.Path != "" {
		return art.Path
	}
	return art.Branch
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Render produces a human-readable status block for the plan registry surface.
func (rs *RegistryStatus) Render() string {
	var b strings.Builder
	if !rs.HasConfig {
		fmt.Fprintln(&b, rs.NextStep)
		return b.String()
	}
	if rs.Total == 0 {
		fmt.Fprintln(&b, "No active Springfield batch.")
		fmt.Fprintln(&b, rs.NextStep)
		return b.String()
	}

	fmt.Fprintln(&b, "No active Springfield batch.")
	fmt.Fprintf(&b, "Plans: %d configured, %d completed\n", rs.Total, rs.Completed)
	if rs.Queue != nil && rs.Queue.Status != QueueIdle {
		fmt.Fprintf(&b, "Queue: %s", rs.Queue.Status)
		if rs.Queue.ActivePlanID != "" {
			fmt.Fprintf(&b, " (%d of %d)", rs.Completed, rs.Total)
		}
		fmt.Fprintln(&b)
		if rs.Queue.ActivePlanID != "" {
			fmt.Fprintf(&b, "Active plan: %s\n", rs.Queue.ActivePlanID)
		}
		if rs.Queue.StopReason != "" {
			fmt.Fprintf(&b, "Stop reason: %s\n", rs.Queue.StopReason)
		}
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Plan registry:")
	for i, p := range rs.Units {
		title := p.Unit.Title
		if title == "" {
			title = p.Unit.ID
		}
		fmt.Fprintf(&b, "  %d. %s  %s  %s\n", i+1, p.Unit.ID, p.Status, title)
		fmt.Fprintf(&b, "     path: %s\n", p.Unit.Path)
		if p.WorktreePath != "" {
			fmt.Fprintf(&b, "     worktree: %s\n", p.WorktreePath)
		}
		if p.Branch != "" {
			fmt.Fprintf(&b, "     branch: %s (base %s @ %s)\n", p.Branch, p.BaseRef, shortSHA(p.BaseHead))
		}
		if p.PlanHead != "" {
			fmt.Fprintf(&b, "     plan head: %s\n", shortSHA(p.PlanHead))
		}
		if p.ExitReason != "" {
			fmt.Fprintf(&b, "     exit: %s\n", p.ExitReason)
		}
		if p.Error != "" {
			fmt.Fprintf(&b, "     error: %s\n", p.Error)
		}
		if p.EvidencePath != "" {
			fmt.Fprintf(&b, "     evidence: %s\n", p.EvidencePath)
		}
		renderMerge(&b, p.Merge)
		renderCleanup(&b, p.Cleanup)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Next step: %s\n", rs.NextStep)
	return b.String()
}
