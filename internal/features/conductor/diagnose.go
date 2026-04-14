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
	NextStep  string
}

// Diagnose inspects project state and returns internal guidance for conductor state.
func Diagnose(project *Project) *Diagnosis {
	schedule := BuildSchedule(project.Config)
	completed, total := schedule.Progress(project.State)

	failures := make([]PlanFailure, 0)
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
	}

	done := schedule.IsComplete(project.State)
	nextStep := "Run: springfield resume"
	switch {
	case total == 0:
		nextStep = "No plans configured. Add plans to your conductor config."
	case done:
		nextStep = "All plans completed successfully."
	case len(failures) > 0:
		nextStep = "Fix failures then run: springfield resume"
	}

	return &Diagnosis{
		Completed: completed,
		Total:     total,
		Done:      done,
		Failures:  failures,
		NextStep:  nextStep,
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

	if d.Done {
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

	fmt.Fprintf(&builder, "\nNext step: %s\n", d.NextStep)
	return builder.String()
}
