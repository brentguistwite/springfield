package ralph_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/features/ralph"
)

type fakeExecutor struct {
	err error
}

func (f fakeExecutor) Execute(ralph.Story) error {
	return f.err
}

func TestWorkspaceRunNextPersistsPassedRun(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open root workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap", Passed: true},
			{ID: "US-002", Title: "Refresh", DependsOn: []string{"US-001"}},
		},
	}
	if err := workspace.InitPlan("refresh", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	record, err := workspace.RunNext("refresh", fakeExecutor{})
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if record.StoryID != "US-002" {
		t.Fatalf("expected story US-002, got %s", record.StoryID)
	}

	if record.Status != "passed" {
		t.Fatalf("expected passed status, got %s", record.Status)
	}

	plan, err := workspace.LoadPlan("refresh")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	story, ok := plan.FindStory("US-002")
	if !ok || !story.Passed {
		t.Fatal("expected US-002 to be marked passed")
	}

	runs, err := workspace.ListRuns()
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}

	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}

	if runs[0].ID != record.ID {
		t.Fatalf("expected persisted run %s, got %s", record.ID, runs[0].ID)
	}
}

func TestWorkspaceRunNextPersistsFailedRun(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open root workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Refresh"},
		},
	}
	if err := workspace.InitPlan("refresh", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	record, err := workspace.RunNext("refresh", fakeExecutor{err: errors.New("runner failed")})
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if record.Status != "failed" {
		t.Fatalf("expected failed status, got %s", record.Status)
	}

	if record.Error != "runner failed" {
		t.Fatalf("expected runner failed error, got %q", record.Error)
	}

	plan, err := workspace.LoadPlan("refresh")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	story, ok := plan.FindStory("US-001")
	if !ok {
		t.Fatal("expected US-001 to exist")
	}

	if story.Passed {
		t.Fatal("expected failed story to remain unpassed")
	}
}

func TestWorkspaceRunNextLeavesPlanUnchangedWhenRunPersistenceFails(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open root workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "springfield",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Refresh"},
		},
	}
	if err := workspace.InitPlan("refresh", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	runsPath := filepath.Join(rootDir, ".springfield", "ralph", "runs")
	if err := os.MkdirAll(filepath.Dir(runsPath), 0o755); err != nil {
		t.Fatalf("create Ralph dir: %v", err)
	}
	if err := os.WriteFile(runsPath, []byte("blocker"), 0o644); err != nil {
		t.Fatalf("write runs blocker: %v", err)
	}

	_, err = workspace.RunNext("refresh", fakeExecutor{})
	if err == nil {
		t.Fatal("expected run persistence failure")
	}

	plan, err := workspace.LoadPlan("refresh")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	story, ok := plan.FindStory("US-001")
	if !ok {
		t.Fatal("expected US-001 to exist")
	}

	if story.Passed {
		t.Fatal("expected US-001 to remain unpassed when run persistence fails")
	}
}

func TestWorkspaceListRunsReturnsStableOrder(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open root workspace: %v", err)
	}

	first := ralph.RunRecord{
		ID:        "refresh-US-001-0001",
		PlanName:  "refresh",
		StoryID:   "US-001",
		Status:    "passed",
		StartedAt: time.Unix(100, 0).UTC(),
		EndedAt:   time.Unix(101, 0).UTC(),
	}
	second := ralph.RunRecord{
		ID:        "refresh-US-002-0002",
		PlanName:  "refresh",
		StoryID:   "US-002",
		Status:    "failed",
		StartedAt: time.Unix(200, 0).UTC(),
		EndedAt:   time.Unix(201, 0).UTC(),
	}

	if err := workspace.SaveRun(first); err != nil {
		t.Fatalf("save first run: %v", err)
	}

	if err := workspace.SaveRun(second); err != nil {
		t.Fatalf("save second run: %v", err)
	}

	runs, err := workspace.ListRuns()
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}

	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	if runs[0].ID != first.ID || runs[1].ID != second.ID {
		t.Fatalf("expected stable ascending order, got %+v", runs)
	}
}
