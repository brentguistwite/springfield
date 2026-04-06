package conductor_test

import (
	"testing"

	"springfield/internal/features/conductor"
)

func TestNextPlansSequentialOnly(t *testing.T) {
	schedule := conductor.BuildSchedule(sequentialOnlyConfig())
	state := conductor.NewState()

	next := schedule.NextPlans(state)
	if len(next) != 1 || next[0] != "01-bootstrap" {
		t.Fatalf("next plans: got %v want [01-bootstrap]", next)
	}

	state.Plans["01-bootstrap"] = &conductor.PlanState{Status: conductor.StatusCompleted}
	next = schedule.NextPlans(state)
	if len(next) != 1 || next[0] != "02-config" {
		t.Fatalf("next plans after completion: got %v want [02-config]", next)
	}
}

func TestNextPlansBatchPhaseReturnsAllIncompletePlans(t *testing.T) {
	schedule := conductor.BuildSchedule(mixedConfig())
	state := conductor.NewState()
	state.Plans["seq-1"] = &conductor.PlanState{Status: conductor.StatusCompleted}
	state.Plans["seq-2"] = &conductor.PlanState{Status: conductor.StatusCompleted}

	next := schedule.NextPlans(state)
	if len(next) != 2 || next[0] != "batch-a" || next[1] != "batch-b" {
		t.Fatalf("batch next plans: got %v want [batch-a batch-b]", next)
	}

	state.Plans["batch-a"] = &conductor.PlanState{Status: conductor.StatusCompleted}
	next = schedule.NextPlans(state)
	if len(next) != 1 || next[0] != "batch-b" {
		t.Fatalf("partial batch next plans: got %v want [batch-b]", next)
	}
}

func TestNextPlansReturnsFailedPlanForResume(t *testing.T) {
	schedule := conductor.BuildSchedule(sequentialOnlyConfig())
	state := conductor.NewState()
	state.Plans["01-bootstrap"] = &conductor.PlanState{Status: conductor.StatusFailed, Error: "boom"}

	next := schedule.NextPlans(state)
	if len(next) != 1 || next[0] != "01-bootstrap" {
		t.Fatalf("next plans: got %v want [01-bootstrap]", next)
	}
}

func TestScheduleProgressAndCompletion(t *testing.T) {
	schedule := conductor.BuildSchedule(mixedConfig())
	state := conductor.NewState()

	completed, total := schedule.Progress(state)
	if completed != 0 || total != 5 {
		t.Fatalf("progress: got %d/%d want 0/5", completed, total)
	}
	if schedule.IsComplete(state) {
		t.Fatal("expected incomplete schedule")
	}

	for _, name := range []string{"seq-1", "seq-2", "batch-a", "batch-b", "batch-c"} {
		state.Plans[name] = &conductor.PlanState{Status: conductor.StatusCompleted}
	}

	completed, total = schedule.Progress(state)
	if completed != 5 || total != 5 {
		t.Fatalf("progress after completion: got %d/%d want 5/5", completed, total)
	}
	if !schedule.IsComplete(state) {
		t.Fatal("expected complete schedule")
	}
}
