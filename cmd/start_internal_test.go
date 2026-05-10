package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
)

// TestBatchNextPlanIDSkipsNonBatchPlans verifies that batchNextPlanID returns
// the first non-integrated plan in the batch's phase order, ignoring plans that
// are not part of this batch (the core of bug #1: non-batch plans in the global
// schedule previously caused runBatch to exit prematurely).
func TestBatchNextPlanIDSkipsNonBatchPlans(t *testing.T) {
	b := batch.Batch{
		ID: "test-batch",
		Phases: []batch.Phase{
			{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}},
			{Mode: batch.PhaseSerial, Plans: []string{"plan-b"}},
		},
		PlanIDs: []string{"plan-a", "plan-b"},
	}

	// State: plan-a and plan-b are both not yet integrated.
	// The global conductor state also has "plan-x" (not in this batch) as
	// integrated — this should not affect batch dispatch.
	state := &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"plan-x": {
				Status:  conductor.StatusCompleted,
				Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
		},
	}

	got := batchNextPlanID(b, state)
	if got != "plan-a" {
		t.Errorf("batchNextPlanID: want plan-a (first non-integrated batch plan), got %q", got)
	}
}

// TestBatchNextPlanIDAdvancesPhase verifies that once the first phase's plans
// are integrated, batchNextPlanID returns a plan from the next phase.
func TestBatchNextPlanIDAdvancesPhase(t *testing.T) {
	b := batch.Batch{
		ID: "test-batch",
		Phases: []batch.Phase{
			{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}},
			{Mode: batch.PhaseSerial, Plans: []string{"plan-b"}},
		},
		PlanIDs: []string{"plan-a", "plan-b"},
	}

	// plan-a is integrated; plan-b is not.
	state := &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"plan-a": {
				Status:  conductor.StatusCompleted,
				Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
		},
	}

	got := batchNextPlanID(b, state)
	if got != "plan-b" {
		t.Errorf("batchNextPlanID: want plan-b after plan-a integrated, got %q", got)
	}
}

// TestBatchNextPlanIDAllIntegratedReturnsEmpty verifies that when every batch
// plan is integrated, batchNextPlanID returns "" to signal completion.
func TestBatchNextPlanIDAllIntegratedReturnsEmpty(t *testing.T) {
	b := batch.Batch{
		ID: "test-batch",
		Phases: []batch.Phase{
			{Mode: batch.PhaseSerial, Plans: []string{"plan-a", "plan-b"}},
		},
		PlanIDs: []string{"plan-a", "plan-b"},
	}

	state := &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"plan-a": {
				Status:  conductor.StatusCompleted,
				Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
			"plan-b": {
				Status:  conductor.StatusCompleted,
				Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
		},
	}

	got := batchNextPlanID(b, state)
	if got != "" {
		t.Errorf("batchNextPlanID: want empty when all integrated, got %q", got)
	}
}

// TestRunBatchWithContextCancelledReturnsContextCanceled verifies that
// runBatchWithContext returns context.Canceled (not nil) when the context is
// already cancelled before the loop runs. The caller must not archive or clear
// run.json on this return value.
func TestRunBatchWithContextCancelledReturnsContextCanceled(t *testing.T) {
	root := t.TempDir()

	// Minimal springfield.toml with agent config.
	if err := os.WriteFile(
		root+"/springfield.toml",
		[]byte("[project]\nagent_priority = [\"claude\"]\n"),
		0o644,
	); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	// Write run.json so we can verify it's untouched.
	run := batch.Run{ActiveBatchID: "test-batch"}
	if err := batch.WriteRun(root, run); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	b := batch.Batch{
		ID:      "test-batch",
		Title:   "Test",
		PlanIDs: []string{"plan-a"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	_, err := runBatchWithContext(ctx, root, run, b, io.Discard, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// run.json must still exist (not cleared).
	if _, statErr := os.Stat(batch.RunPath(root)); statErr != nil {
		t.Errorf("run.json should still exist after interrupt: %v", statErr)
	}
}

// TestBatchNextPlanIDPhaseBlocksUntilComplete verifies serial phase semantics:
// if the first phase has a non-integrated plan, the second phase's plans are
// not dispatched even if the second phase plans exist.
func TestBatchNextPlanIDPhaseBlocksUntilComplete(t *testing.T) {
	b := batch.Batch{
		ID: "test-batch",
		Phases: []batch.Phase{
			{Mode: batch.PhaseSerial, Plans: []string{"plan-a", "plan-c"}},
			{Mode: batch.PhaseSerial, Plans: []string{"plan-b"}},
		},
		PlanIDs: []string{"plan-a", "plan-b", "plan-c"},
	}

	// plan-a integrated, plan-c not integrated yet — phase 0 not complete.
	state := &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"plan-a": {
				Status:  conductor.StatusCompleted,
				Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
		},
	}

	got := batchNextPlanID(b, state)
	// Should return plan-c (next non-integrated in phase 0), not plan-b (phase 1).
	if got != "plan-c" {
		t.Errorf("batchNextPlanID: want plan-c (phase 0 not complete), got %q", got)
	}
}
