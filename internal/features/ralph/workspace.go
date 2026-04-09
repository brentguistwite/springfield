package ralph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"springfield/internal/storage"
)

const (
	singlePlansDir = "execution/single/plans"
	singleRunsDir  = "execution/single/runs"
	legacyPlansDir = "ralph/plans"
	legacyRunsDir  = "ralph/runs"
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
		now:     func() time.Time { return time.Now().UTC() },
	}, nil
}

// OpenRootForTest creates a workspace with an injectable clock for testing.
func OpenRootForTest(rootDir string, clock func() time.Time) (Workspace, error) {
	runtime, err := storage.FromRoot(rootDir)
	if err != nil {
		return Workspace{}, fmt.Errorf("open Ralph workspace: %w", err)
	}

	return Workspace{
		runtime: runtime,
		now:     clock,
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
		now:     func() time.Time { return time.Now().UTC() },
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
	if err := w.readJSONWithFallback(planPath(name), legacyPlanPath(name), &plan); err != nil {
		return Plan{}, fmt.Errorf("load Ralph plan %q: %w", name, err)
	}

	return plan, nil
}

// ListPlans returns all persisted Ralph plans in stable order.
func (w Workspace) ListPlans() ([]Plan, error) {
	planPaths, err := w.listJSONWithFallback(singlePlansDir, legacyPlansDir)
	if err != nil {
		return nil, fmt.Errorf("list Ralph plans: %w", err)
	}

	plans := make([]Plan, 0, len(planPaths))
	for _, planPath := range planPaths {
		var plan Plan
		if err := w.runtime.ReadJSON(planPath, &plan); err != nil {
			return nil, fmt.Errorf("load Ralph plan %s: %w", filepath.Base(planPath), err)
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
	runPaths, err := w.listJSONWithFallback(singleRunsDir, legacyRunsDir)
	if err != nil {
		return nil, fmt.Errorf("list Ralph runs: %w", err)
	}

	runs := make([]RunRecord, 0, len(runPaths))
	for _, runPath := range runPaths {
		var record RunRecord
		if err := w.runtime.ReadJSON(runPath, &record); err != nil {
			return nil, fmt.Errorf("load Ralph run %s: %w", filepath.Base(runPath), err)
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
	record.Stdout = result.Stdout
	record.Stderr = result.Stderr
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

// ResetStories clears the Passed flag for the given story IDs.
// If no IDs are provided, all stories in the plan are reset.
func (w Workspace) ResetStories(planName string, storyIDs ...string) error {
	plan, err := w.LoadPlan(planName)
	if err != nil {
		return err
	}

	if len(storyIDs) == 0 {
		changed := false
		for i := range plan.Spec.Stories {
			if !plan.Spec.Stories[i].Passed {
				continue
			}
			plan.Spec.Stories[i].Passed = false
			changed = true
		}

		if !changed {
			return nil
		}

		return w.runtime.WriteJSON(planPath(planName), plan)
	}

	indexByID := make(map[string]int, len(plan.Spec.Stories))
	for i, story := range plan.Spec.Stories {
		indexByID[story.ID] = i
	}

	targets := make(map[string]bool, len(storyIDs))
	for _, id := range storyIDs {
		if targets[id] {
			continue
		}
		targets[id] = true

		index, ok := indexByID[id]
		if !ok {
			return fmt.Errorf("story %q not found in Ralph plan %q", id, planName)
		}
		if !plan.Spec.Stories[index].Passed {
			return fmt.Errorf("story %q is already pending in Ralph plan %q", id, planName)
		}
	}

	for id := range targets {
		plan.Spec.Stories[indexByID[id]].Passed = false
	}

	return w.runtime.WriteJSON(planPath(planName), plan)
}

func planPath(name string) string {
	return filepath.Join(singlePlansDir, sanitizeName(name)+".json")
}

func runPath(id string) string {
	return filepath.Join(singleRunsDir, sanitizeName(id)+".json")
}

func legacyPlanPath(name string) string {
	return filepath.Join(legacyPlansDir, sanitizeName(name)+".json")
}

func legacyRunPath(id string) string {
	return filepath.Join(legacyRunsDir, sanitizeName(id)+".json")
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

func (w Workspace) readJSONWithFallback(primaryPath string, legacyPath string, target any) error {
	if err := w.runtime.ReadJSON(primaryPath, target); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return w.runtime.ReadJSON(legacyPath, target)
}

func (w Workspace) listJSONWithFallback(primaryDir string, legacyDir string) ([]string, error) {
	paths := make([]string, 0)
	seen := make(map[string]bool)

	for _, dir := range []string{primaryDir, legacyDir} {
		dirPath, err := w.runtime.Path(dir)
		if err != nil {
			return nil, err
		}

		entries, err := os.ReadDir(dirPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" || seen[entry.Name()] {
				continue
			}

			seen[entry.Name()] = true
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}

	return paths, nil
}
