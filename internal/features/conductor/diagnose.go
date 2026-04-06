package conductor

import (
	"fmt"
	"strings"
)

// PlanFailure describes one failed plan plus its error.
type PlanFailure struct {
	Plan  string
	Error string
}

// Diagnosis summarizes current conductor progress and next action.
type Diagnosis struct {
	Completed int
	Total     int
	Done      bool
	Failures  []PlanFailure
	NextStep  string
}

// Diagnose inspects project state and returns user-facing guidance.
func Diagnose(project *Project) *Diagnosis {
	schedule := BuildSchedule(project.Config)
	completed, total := schedule.Progress(project.State)

	failures := make([]PlanFailure, 0)
	for _, name := range project.AllPlans() {
		if project.PlanStatus(name) == StatusFailed {
			failures = append(failures, PlanFailure{
				Plan:  name,
				Error: project.PlanError(name),
			})
		}
	}

	done := schedule.IsComplete(project.State)
	nextStep := "Run: springfield conductor run"
	switch {
	case done:
		nextStep = "All plans completed successfully."
	case len(failures) > 0:
		nextStep = "Fix failures then run: springfield conductor resume"
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

	if d.Done {
		builder.WriteString("Status: all plans completed\n")
		return builder.String()
	}

	if len(d.Failures) > 0 {
		fmt.Fprintf(&builder, "\nFailed plans (%d):\n", len(d.Failures))
		for _, failure := range d.Failures {
			fmt.Fprintf(&builder, "  - %s: %s\n", failure.Plan, failure.Error)
		}
	}

	fmt.Fprintf(&builder, "\nNext step: %s\n", d.NextStep)
	return builder.String()
}
