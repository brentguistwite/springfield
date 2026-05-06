package conductor_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/conductor"
)

func TestQueueStateRoundTrip(t *testing.T) {
	now := time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)
	state := &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {Status: conductor.StatusCompleted},
		},
		Queue: &conductor.QueueState{
			Status:       conductor.QueueRunning,
			ActivePlanID: "beta",
			StartedAt:    now,
			Heartbeat:    now,
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got conductor.State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Queue == nil {
		t.Fatal("Queue is nil after round-trip")
	}
	if got.Queue.Status != conductor.QueueRunning {
		t.Fatalf("status = %q, want %q", got.Queue.Status, conductor.QueueRunning)
	}
	if got.Queue.ActivePlanID != "beta" {
		t.Fatalf("active_plan_id = %q, want beta", got.Queue.ActivePlanID)
	}
	if !got.Queue.StartedAt.Equal(now) {
		t.Fatalf("started_at = %v, want %v", got.Queue.StartedAt, now)
	}
}

func TestQueueStateNilWhenAbsentInJSON(t *testing.T) {
	data := []byte(`{"plans":{}}`)
	var state conductor.State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if state.Queue != nil {
		t.Fatalf("Queue should be nil for legacy state, got %+v", state.Queue)
	}
}

func TestNormalizeStaleRunningResetsQueueToIdle(t *testing.T) {
	now := time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	project.State.Queue = &conductor.QueueState{
		Status:       conductor.QueueRunning,
		ActivePlanID: "alpha",
		StartedAt:    now.Add(-10 * time.Minute),
		Heartbeat:    now.Add(-5 * time.Minute),
	}

	project.NormalizeStaleRunning(now)

	if project.State.Queue.Status != conductor.QueueIdle {
		t.Fatalf("queue status = %q, want idle", project.State.Queue.Status)
	}
	if project.State.Queue.ActivePlanID != "" {
		t.Fatalf("active_plan_id should be cleared, got %q", project.State.Queue.ActivePlanID)
	}
	if !project.State.Queue.EndedAt.Equal(now) {
		t.Fatalf("ended_at = %v, want %v", project.State.Queue.EndedAt, now)
	}
}

func TestNormalizeStaleRunningLeavesNonRunningQueueAlone(t *testing.T) {
	now := time.Date(2025, 5, 1, 12, 0, 0, 0, time.UTC)
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	project.State.Queue = &conductor.QueueState{
		Status:     conductor.QueueHalted,
		StopReason: "plan failed",
	}

	project.NormalizeStaleRunning(now)

	if project.State.Queue.Status != conductor.QueueHalted {
		t.Fatalf("queue status = %q, want halted (should be unchanged)", project.State.Queue.Status)
	}
}

func TestBuildRegistryStatusShowsQueueRunning(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	writeFile(t, filepath.Join(root, plansDir, "second.md"), "# second")
	writeFile(t, filepath.Join(root, plansDir, "third.md"), "# third")
	project := loadProjectWithDefaults(t, root, plansDir)

	project.Config.PlanUnits = []conductor.PlanUnit{
		{ID: "alpha", Path: plansDir + "/feature.md", Order: 1},
		{ID: "beta", Path: plansDir + "/second.md", Order: 2},
		{ID: "gamma", Path: plansDir + "/third.md", Order: 3},
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge:  &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}
	project.State.Queue = &conductor.QueueState{
		Status:       conductor.QueueRunning,
		ActivePlanID: "beta",
	}

	rs := conductor.BuildRegistryStatus(project)
	rendered := rs.Render()

	if !strings.Contains(rendered, "Queue: running") {
		t.Errorf("expected queue running in output:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Active plan: beta") {
		t.Errorf("expected active plan beta:\n%s", rendered)
	}
	if !strings.Contains(rendered, "1 of 3") {
		t.Errorf("expected progress 1 of 3:\n%s", rendered)
	}
}

func TestBuildRegistryStatusShowsQueueHalted(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	writeFile(t, filepath.Join(root, plansDir, "second.md"), "# second")
	project := loadProjectWithDefaults(t, root, plansDir)

	project.Config.PlanUnits = []conductor.PlanUnit{
		{ID: "alpha", Path: plansDir + "/feature.md", Order: 1},
		{ID: "beta", Path: plansDir + "/second.md", Order: 2},
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusFailed,
		Error:  "agent exit 1",
	}
	project.State.Queue = &conductor.QueueState{
		Status:     conductor.QueueHalted,
		StopReason: "plan alpha failed",
	}

	rs := conductor.BuildRegistryStatus(project)
	rendered := rs.Render()

	if !strings.Contains(rendered, "Queue: halted") {
		t.Errorf("expected queue halted:\n%s", rendered)
	}
	if !strings.Contains(rendered, "plan alpha failed") {
		t.Errorf("expected stop reason:\n%s", rendered)
	}
}

func TestBuildRegistryStatusShowsQueueCompleted(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	project.Config.PlanUnits = []conductor.PlanUnit{
		{ID: "alpha", Path: plansDir + "/feature.md", Order: 1},
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge:  &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}
	project.State.Queue = &conductor.QueueState{
		Status: conductor.QueueCompleted,
	}

	rs := conductor.BuildRegistryStatus(project)
	rendered := rs.Render()

	if !strings.Contains(rendered, "Queue: completed") {
		t.Errorf("expected queue completed:\n%s", rendered)
	}
}

func TestQueueStateHaltedSerializesStopReason(t *testing.T) {
	state := &conductor.State{
		Plans: make(map[string]*conductor.PlanState),
		Queue: &conductor.QueueState{
			Status:       conductor.QueueHalted,
			ActivePlanID: "gamma",
			StopReason:   "plan gamma failed: agent exit code 1",
		},
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got conductor.State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Queue.Status != conductor.QueueHalted {
		t.Fatalf("status = %q, want halted", got.Queue.Status)
	}
	if got.Queue.StopReason != "plan gamma failed: agent exit code 1" {
		t.Fatalf("stop_reason = %q", got.Queue.StopReason)
	}
}
