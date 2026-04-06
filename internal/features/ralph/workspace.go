package ralph

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"springfield/internal/storage"
)

// Workspace owns Ralph state for one Springfield project root.
type Workspace struct {
	runtime storage.Runtime
	now     func() time.Time
}

// OpenRoot opens Ralph state from an explicit project root.
func OpenRoot(rootDir string) (Workspace, error) {
	runtime, err := storage.FromRoot(rootDir)
	if err != nil {
		return Workspace{}, fmt.Errorf("open Ralph workspace: %w", err)
	}

	return Workspace{
		runtime: runtime,
		now:     time.Now().UTC,
	}, nil
}

// Open resolves Ralph state from the current directory upward.
func Open(startDir string) (Workspace, error) {
	runtime, err := storage.ResolveFrom(startDir)
	if err != nil {
		return Workspace{}, fmt.Errorf("open Ralph workspace: %w", err)
	}

	return Workspace{
		runtime: runtime,
		now:     time.Now().UTC,
	}, nil
}

// InitPlan persists a new Ralph plan definition.
func (w Workspace) InitPlan(name string, spec Spec) error {
	plan := Plan{
		Name: name,
		Spec: spec,
	}

	return w.runtime.WriteJSON(planPath(name), plan)
}

// LoadPlan reads a previously persisted Ralph plan.
func (w Workspace) LoadPlan(name string) (Plan, error) {
	var plan Plan
	if err := w.runtime.ReadJSON(planPath(name), &plan); err != nil {
		return Plan{}, fmt.Errorf("load Ralph plan %q: %w", name, err)
	}

	return plan, nil
}

// ListPlans returns all persisted Ralph plans in stable order.
func (w Workspace) ListPlans() ([]Plan, error) {
	plansDir, err := w.runtime.Path("ralph", "plans")
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(plansDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("list Ralph plans: %w", err)
	}

	plans := make([]Plan, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		var plan Plan
		if err := w.runtime.ReadJSON(filepath.Join("ralph", "plans", entry.Name()), &plan); err != nil {
			return nil, fmt.Errorf("load Ralph plan %s: %w", entry.Name(), err)
		}

		plans = append(plans, plan)
	}

	slices.SortFunc(plans, func(left, right Plan) int {
		if left.Name < right.Name {
			return -1
		}
		if left.Name > right.Name {
			return 1
		}
		return 0
	})

	return plans, nil
}

// SaveRun persists one Ralph run record.
func (w Workspace) SaveRun(record RunRecord) error {
	return w.runtime.WriteJSON(runPath(record.ID), record)
}

// ListRuns returns all persisted Ralph run records in stable order.
func (w Workspace) ListRuns() ([]RunRecord, error) {
	runsDir, err := w.runtime.Path("ralph", "runs")
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("list Ralph runs: %w", err)
	}

	runs := make([]RunRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		var record RunRecord
		if err := w.runtime.ReadJSON(filepath.Join("ralph", "runs", entry.Name()), &record); err != nil {
			return nil, fmt.Errorf("load Ralph run %s: %w", entry.Name(), err)
		}

		runs = append(runs, record)
	}

	slices.SortFunc(runs, func(left, right RunRecord) int {
		if left.StartedAt.Before(right.StartedAt) {
			return -1
		}
		if left.StartedAt.After(right.StartedAt) {
			return 1
		}
		if left.ID < right.ID {
			return -1
		}
		if left.ID > right.ID {
			return 1
		}
		return 0
	})

	return runs, nil
}

// RunNext executes the next eligible story and persists the run result.
func (w Workspace) RunNext(planName string, executor StoryExecutor) (RunRecord, error) {
	plan, err := w.LoadPlan(planName)
	if err != nil {
		return RunRecord{}, err
	}

	story, ok := plan.NextEligible()
	if !ok {
		return RunRecord{}, fmt.Errorf("no eligible story in Ralph plan %q", planName)
	}

	startedAt := w.now()
	record := RunRecord{
		ID:        fmt.Sprintf("%s-%s-%d", sanitizeName(planName), sanitizeName(story.ID), startedAt.UnixMilli()),
		PlanName:  planName,
		StoryID:   story.ID,
		StartedAt: startedAt,
	}

	result := executor.Execute(story)
	record.EndedAt = w.now()
	record.Agent = result.Agent
	record.ExitCode = result.ExitCode
	if result.Err != nil {
		record.Status = "failed"
		record.Error = result.Err.Error()
	} else {
		record.Status = "passed"
		if err := w.setStoryPassed(planName, story.ID, true); err != nil {
			return RunRecord{}, err
		}
	}

	if err := w.SaveRun(record); err != nil {
		if record.Status == "passed" {
			if rollbackErr := w.setStoryPassed(planName, story.ID, false); rollbackErr != nil {
				return RunRecord{}, fmt.Errorf("persist Ralph run: %w (rollback failed: %v)", err, rollbackErr)
			}
		}
		return RunRecord{}, fmt.Errorf("persist Ralph run: %w", err)
	}

	return record, nil
}

func planPath(name string) string {
	return filepath.Join("ralph", "plans", sanitizeName(name)+".json")
}

func runPath(id string) string {
	return filepath.Join("ralph", "runs", sanitizeName(id)+".json")
}

func sanitizeName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "plan"
	}

	return trimmed
}

func (w Workspace) setStoryPassed(planName string, storyID string, passed bool) error {
	plan, err := w.LoadPlan(planName)
	if err != nil {
		return err
	}

	for index := range plan.Spec.Stories {
		if plan.Spec.Stories[index].ID == storyID {
			plan.Spec.Stories[index].Passed = passed
			return w.runtime.WriteJSON(planPath(planName), plan)
		}
	}

	return fmt.Errorf("story %q not found in Ralph plan %q", storyID, planName)
}
