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
	result ralph.RunResult
}

func newPassingExecutor(agent string) fakeExecutor {
	return fakeExecutor{result: ralph.RunResult{Agent: agent, ExitCode: 0}}
}

func newFailingExecutor(agent string, exitCode int, err error) fakeExecutor {
	return fakeExecutor{result: ralph.RunResult{Agent: agent, ExitCode: exitCode, Err: err}}
}

func (f fakeExecutor) Execute(ralph.Story) ralph.RunResult {
	return f.result
}

func TestWorkspaceRunNextPersistsAgentOutput(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "test",
		Stories: []ralph.Story{{ID: "US-001", Title: "Do thing"}},
	}
	if err := workspace.InitPlan("output", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	executor := fakeExecutor{result: ralph.RunResult{
		Agent:  "claude",
		Stdout: `{"type":"result","subtype":"success"}`,
		Stderr: "warning: something",
	}}

	record, err := workspace.RunNext("output", executor)
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if record.Stdout != `{"type":"result","subtype":"success"}` {
		t.Fatalf("expected stdout preserved, got %q", record.Stdout)
	}
	if record.Stderr != "warning: something" {
		t.Fatalf("expected stderr preserved, got %q", record.Stderr)
	}

	// Verify persisted record also has output
	runs, err := workspace.ListRuns()
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Stdout != record.Stdout {
		t.Fatalf("persisted stdout mismatch: %q", runs[0].Stdout)
	}
	if runs[0].Stderr != record.Stderr {
		t.Fatalf("persisted stderr mismatch: %q", runs[0].Stderr)
	}
}

func TestWorkspaceRunNextRecordsDistinctTimestamps(t *testing.T) {
	rootDir := t.TempDir()
	tick := 0
	clock := func() time.Time {
		tick++
		return time.Date(2026, 4, 8, 0, 0, tick, 0, time.UTC)
	}

	workspace, err := ralph.OpenRootForTest(rootDir, clock)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "test",
		Stories: []ralph.Story{{ID: "US-001", Title: "Do thing"}},
	}
	if err := workspace.InitPlan("ts", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	record, err := workspace.RunNext("ts", newPassingExecutor("claude"))
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if !record.StartedAt.Before(record.EndedAt) {
		t.Fatalf("expected StartedAt < EndedAt, got %v and %v", record.StartedAt, record.EndedAt)
	}
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

	record, err := workspace.RunNext("refresh", newPassingExecutor("claude"))
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if record.StoryID != "US-002" {
		t.Fatalf("expected story US-002, got %s", record.StoryID)
	}

	if record.Status != "passed" {
		t.Fatalf("expected passed status, got %s", record.Status)
	}

	if record.Agent != "claude" {
		t.Fatalf("expected agent claude, got %q", record.Agent)
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

	if runs[0].Agent != "claude" {
		t.Fatalf("expected persisted agent claude, got %q", runs[0].Agent)
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

	record, err := workspace.RunNext("refresh", newFailingExecutor("codex", 1, errors.New("runner failed")))
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if record.Status != "failed" {
		t.Fatalf("expected failed status, got %s", record.Status)
	}

	if record.Agent != "codex" {
		t.Fatalf("expected agent codex, got %q", record.Agent)
	}

	if record.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", record.ExitCode)
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

	runsPath := filepath.Join(rootDir, ".springfield", "execution", "single", "runs")
	if err := os.MkdirAll(filepath.Dir(runsPath), 0o755); err != nil {
		t.Fatalf("create Springfield single-run dir: %v", err)
	}
	if err := os.WriteFile(runsPath, []byte("blocker"), 0o644); err != nil {
		t.Fatalf("write runs blocker: %v", err)
	}

	_, err = workspace.RunNext("refresh", newPassingExecutor("claude"))
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

func TestWorkspaceResetSpecificStory(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "test",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "First", Passed: true},
			{ID: "US-002", Title: "Second", Passed: true},
		},
	}
	if err := workspace.InitPlan("reset", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	if err := workspace.ResetStories("reset", "US-001"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	plan, err := workspace.LoadPlan("reset")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	s1, _ := plan.FindStory("US-001")
	if s1.Passed {
		t.Fatal("expected US-001 to be reset")
	}

	s2, _ := plan.FindStory("US-002")
	if !s2.Passed {
		t.Fatal("expected US-002 to remain passed")
	}
}

func TestWorkspaceResetSpecificStoryReportsUnknownID(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "test",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "First", Passed: true},
		},
	}
	if err := workspace.InitPlan("reset", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	err = workspace.ResetStories("reset", "US-999")
	if err == nil {
		t.Fatal("expected unknown story reset to fail")
	}
	if err.Error() != `story "US-999" not found in Ralph plan "reset"` {
		t.Fatalf("reset: %v", err)
	}
}

func TestWorkspaceResetSpecificStoryReportsAlreadyPending(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "test",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "First", Passed: false},
		},
	}
	if err := workspace.InitPlan("reset", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	err = workspace.ResetStories("reset", "US-001")
	if err == nil {
		t.Fatal("expected already-pending story reset to fail")
	}
	if err.Error() != `story "US-001" is already pending in Ralph plan "reset"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceResetAllStories(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "test",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "First", Passed: true},
			{ID: "US-002", Title: "Second", Passed: true},
		},
	}
	if err := workspace.InitPlan("reset-all", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	if err := workspace.ResetStories("reset-all"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	plan, err := workspace.LoadPlan("reset-all")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	for _, story := range plan.Spec.Stories {
		if story.Passed {
			t.Fatalf("expected %s to be reset", story.ID)
		}
	}
}

func TestWorkspaceResetMakesStoryEligibleForRunNext(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}

	spec := ralph.Spec{
		Project: "test",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Only story", Passed: true},
		},
	}
	if err := workspace.InitPlan("re-run", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	// Before reset: no eligible stories
	plan, _ := workspace.LoadPlan("re-run")
	if _, ok := plan.NextEligible(); ok {
		t.Fatal("expected no eligible story before reset")
	}

	if err := workspace.ResetStories("re-run", "US-001"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// After reset: US-001 is eligible again
	record, err := workspace.RunNext("re-run", newPassingExecutor("claude"))
	if err != nil {
		t.Fatalf("run next after reset: %v", err)
	}
	if record.StoryID != "US-001" {
		t.Fatalf("expected US-001, got %s", record.StoryID)
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

func TestWorkspaceLoadPlanFallsBackToLegacyPath(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open root workspace: %v", err)
	}

	legacyPlan := ralph.Plan{
		Name: "legacy",
		Spec: ralph.Spec{
			Project: "springfield",
			Stories: []ralph.Story{{ID: "US-001", Title: "Legacy story"}},
		},
	}

	legacyPath := filepath.Join(rootDir, ".springfield", "ralph", "plans", "legacy.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("create legacy plan dir: %v", err)
	}
	data := []byte("{\n  \"name\": \"legacy\",\n  \"spec\": {\n    \"project\": \"springfield\",\n    \"stories\": [\n      {\n        \"id\": \"US-001\",\n        \"title\": \"Legacy story\"\n      }\n    ]\n  }\n}\n")
	if err := os.WriteFile(legacyPath, data, 0o644); err != nil {
		t.Fatalf("write legacy plan: %v", err)
	}

	plan, err := workspace.LoadPlan("legacy")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	if plan.Name != legacyPlan.Name {
		t.Fatalf("expected plan name %q, got %q", legacyPlan.Name, plan.Name)
	}
	if plan.Spec.Project != legacyPlan.Spec.Project {
		t.Fatalf("expected project %q, got %q", legacyPlan.Spec.Project, plan.Spec.Project)
	}
	if len(plan.Spec.Stories) != 1 || plan.Spec.Stories[0].ID != "US-001" || plan.Spec.Stories[0].Title != "Legacy story" {
		t.Fatalf("expected legacy story to load, got %+v", plan.Spec.Stories)
	}
}

func TestWorkspaceListRunsFallsBackToLegacyPath(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open root workspace: %v", err)
	}

	legacyRun := []byte("{\n  \"id\": \"legacy-run\",\n  \"plan_name\": \"refresh\",\n  \"story_id\": \"US-001\",\n  \"status\": \"failed\",\n  \"started_at\": \"2026-04-08T00:00:00Z\",\n  \"ended_at\": \"2026-04-08T00:01:00Z\"\n}\n")
	legacyPath := filepath.Join(rootDir, ".springfield", "ralph", "runs", "legacy-run.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("create legacy run dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, legacyRun, 0o644); err != nil {
		t.Fatalf("write legacy run: %v", err)
	}

	runs, err := workspace.ListRuns()
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}

	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ID != "legacy-run" {
		t.Fatalf("expected legacy run id, got %q", runs[0].ID)
	}
}
