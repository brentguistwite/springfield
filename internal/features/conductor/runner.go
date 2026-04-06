package conductor

import (
	"errors"
	"fmt"
)

// PlanExecutor is the runner boundary for executing one plan.
type PlanExecutor interface {
	Execute(plan string) error
}

// Runner executes schedule phases against project state.
type Runner struct {
	Project  *Project
	schedule *Schedule
	executor PlanExecutor
}

// NewRunner constructs a runner for project using executor.
func NewRunner(project *Project, executor PlanExecutor) *Runner {
	return &Runner{
		Project:  project,
		schedule: BuildSchedule(project.Config),
		executor: executor,
	}
}

// RunNext executes the next eligible phase and persists state transitions.
func (r *Runner) RunNext() (ran []string, done bool, err error) {
	next := r.schedule.NextPlans(r.Project.State)
	if len(next) == 0 {
		return nil, true, nil
	}

	for _, name := range next {
		r.Project.MarkRunning(name)
	}
	if err := r.Project.SaveState(); err != nil {
		return nil, false, err
	}

	var runErr error
	for _, name := range next {
		ran = append(ran, name)
		if execErr := r.executor.Execute(name); execErr != nil {
			r.Project.MarkFailed(name, execErr.Error())
			if runErr == nil {
				runErr = fmt.Errorf("plan %s: %w", name, execErr)
			}
			continue
		}

		r.Project.MarkCompleted(name)
	}

	done = r.schedule.IsComplete(r.Project.State)
	if saveErr := r.Project.SaveState(); saveErr != nil {
		if runErr != nil {
			return ran, done, errors.Join(runErr, fmt.Errorf("save state: %w", saveErr))
		}
		return ran, done, saveErr
	}

	return ran, done, runErr
}

// RunAll executes remaining phases until completion or failure.
func (r *Runner) RunAll() error {
	for {
		_, done, err := r.RunNext()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}
