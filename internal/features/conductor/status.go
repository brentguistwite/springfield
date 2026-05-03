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
}

// PlanUnitStatus is one ordered plan unit annotated with its current state.
type PlanUnitStatus struct {
	Unit         PlanUnit
	Status       PlanStatus
	Error        string
	Agent        string
	EvidencePath string
	Attempts     int
}

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
	var firstPending string
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
		rs.Units = append(rs.Units, entry)
		rs.Total++
		switch st {
		case StatusCompleted:
			rs.Completed++
		case StatusFailed:
			failures = append(failures, PlanFailure{
				Plan:         id,
				Error:        entry.Error,
				Agent:        entry.Agent,
				EvidencePath: entry.EvidencePath,
				Attempts:     entry.Attempts,
			})
		case StatusPending, StatusRunning:
			if firstPending == "" {
				firstPending = id
			}
		}
	}
	rs.Failures = failures

	switch {
	case len(failures) > 0:
		rs.NextStep = fmt.Sprintf("Fix failures (%s) then run: springfield start", failures[0].Plan)
	case rs.Completed == rs.Total:
		rs.NextStep = "All plans completed."
	case firstPending != "":
		rs.NextStep = fmt.Sprintf("Run: springfield start (next plan: %s)", firstPending)
	default:
		rs.NextStep = "Run: springfield start"
	}
	return rs
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
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Plan registry:")
	for i, p := range rs.Units {
		title := p.Unit.Title
		if title == "" {
			title = p.Unit.ID
		}
		fmt.Fprintf(&b, "  %d. %s  %s  %s\n", i+1, p.Unit.ID, p.Status, title)
		fmt.Fprintf(&b, "     path: %s\n", p.Unit.Path)
		if p.Error != "" {
			fmt.Fprintf(&b, "     error: %s\n", p.Error)
		}
		if p.EvidencePath != "" {
			fmt.Fprintf(&b, "     evidence: %s\n", p.EvidencePath)
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Next step: %s\n", rs.NextStep)
	return b.String()
}
