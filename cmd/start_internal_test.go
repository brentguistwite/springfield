package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// TestPlanDirTamperGuardSnapshotsRunJSON verifies that planDirTamperGuard
// snapshots run.json and detects deletion or modification as tamper.
func TestPlanDirTamperGuardSnapshotsRunJSON(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(root, ".springfield", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir planDir: %v", err)
	}
	// Write a run.json before snapshot.
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "my-batch"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	guard := &planDirTamperGuard{planDir: planDir, controlRoot: root}
	if err := guard.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	t.Run("delete run.json is tamper", func(t *testing.T) {
		if err := os.Remove(batch.RunPath(root)); err != nil {
			t.Fatalf("remove run.json: %v", err)
		}
		reason, err := guard.Detect()
		if err != nil {
			t.Fatalf("Detect: %v", err)
		}
		if reason == "" {
			t.Fatal("expected tamper reason for deleted run.json, got empty")
		}
		// Restore puts it back.
		if err := guard.Restore(); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		data, err := os.ReadFile(batch.RunPath(root))
		if err != nil {
			t.Fatalf("run.json missing after Restore: %v", err)
		}
		if !bytes.Contains(data, []byte("my-batch")) {
			t.Errorf("restored run.json missing expected content: %s", data)
		}
	})

	// Re-snapshot after restore so the next sub-test starts from valid state.
	if err := guard.Snapshot(); err != nil {
		t.Fatalf("re-Snapshot: %v", err)
	}

	t.Run("modify run.json is tamper", func(t *testing.T) {
		if err := os.WriteFile(batch.RunPath(root), []byte(`{"active_batch_id":"corrupted"}`), 0o644); err != nil {
			t.Fatalf("write corrupted run.json: %v", err)
		}
		reason, err := guard.Detect()
		if err != nil {
			t.Fatalf("Detect: %v", err)
		}
		if reason == "" {
			t.Fatal("expected tamper reason for modified run.json, got empty")
		}
		// Restore puts it back to original bytes.
		if err := guard.Restore(); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		data, err := os.ReadFile(batch.RunPath(root))
		if err != nil {
			t.Fatalf("run.json missing after Restore: %v", err)
		}
		if !bytes.Contains(data, []byte("my-batch")) {
			t.Errorf("restored run.json missing expected content: %s", data)
		}
	})
}

// TestPlanDirTamperGuardRunJSONAbsentBeforeSnapshot verifies that when run.json
// does not exist at Snapshot time, deleting it (noop) is not tamper, and creating
// it is tamper (guarded to keep the Snapshot semantics consistent).
func TestPlanDirTamperGuardRunJSONAbsentBeforeSnapshot(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(root, ".springfield", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir planDir: %v", err)
	}
	// No run.json written — doesn't exist at Snapshot time.

	guard := &planDirTamperGuard{planDir: planDir, controlRoot: root}
	if err := guard.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// With no run.json before or after, Detect must report no tamper.
	reason, err := guard.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if reason != "" {
		t.Errorf("expected no tamper when run.json absent in both snapshot and current, got %q", reason)
	}

	// If agent CREATES run.json (it didn't exist before), that's tamper.
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "injected"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	reason, err = guard.Detect()
	if err != nil {
		t.Fatalf("Detect after creation: %v", err)
	}
	if reason == "" {
		t.Fatal("expected tamper when run.json created by agent, got empty")
	}
	// Restore must delete the injected file.
	if err := guard.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, statErr := os.Stat(batch.RunPath(root)); !os.IsNotExist(statErr) {
		t.Errorf("expected run.json to be deleted by Restore, stat error: %v", statErr)
	}
}

// TestRunBatchWithContextMissingExecutionConfigFails verifies that when a batch
// references plans but the conductor execution config is absent (or empty),
// runBatchWithContext returns an error and does NOT archive/clear the batch.
func TestRunBatchWithContextMissingExecutionConfigFails(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"),
		0o644,
	); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	// Write run.json.
	run := batch.Run{ActiveBatchID: "test-batch"}
	if err := batch.WriteRun(root, run); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	// Batch references a plan but we intentionally DO NOT write execution config.
	b := batch.Batch{
		ID:      "test-batch",
		Title:   "Test",
		PlanIDs: []string{"plan-a"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}}},
	}

	ctx := context.Background()
	result, err := runBatchWithContext(ctx, root, run, b, io.Discard, "")
	if err == nil {
		t.Fatal("expected error when execution config is missing, got nil")
	}
	if result.Error == "" {
		t.Fatal("expected BatchRunResult.Error to be set")
	}

	// Batch must still be present (not archived/cleared).
	if _, statErr := os.Stat(batch.RunPath(root)); statErr != nil {
		t.Errorf("run.json should still exist after config-missing failure: %v", statErr)
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
