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

// TestBatchPlanRunnerIsTerminal verifies the batchexec terminal contract at
// the adapter boundary: only a FULLY INTEGRATED plan (merge succeeded +
// cleanup succeeded) is terminal. In particular StatusCompleted alone is NOT
// terminal — such a plan must still be dispatched so RunPlan can drive the
// integration-only re-entry path (a crash between execution and integration
// would otherwise orphan the plan).
func TestBatchPlanRunnerIsTerminal(t *testing.T) {
	project := &conductor.Project{
		State: &conductor.State{
			Plans: map[string]*conductor.PlanState{
				"integrated": {
					Status:  conductor.StatusCompleted,
					Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
					Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
				},
				// Completed with NO merge record: IsIntegrated treats this
				// as integrated (legacy/no-merge-ledger semantics) — same as
				// the old batchNextPlanID predicate.
				"completed-no-merge-record": {
					Status: conductor.StatusCompleted,
				},
				// Completed but integration incomplete (merge pending): the
				// crash-between-execution-and-integration re-entry case.
				"completed-not-integrated": {
					Status: conductor.StatusCompleted,
					Merge:  &conductor.MergeOutcome{Status: conductor.MergePending},
				},
				// Merge succeeded but cleanup never durably recorded.
				"completed-cleanup-missing": {
					Status: conductor.StatusCompleted,
					Merge:  &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
				},
				"running": {
					Status: conductor.StatusRunning,
				},
			},
		},
	}
	r := &batchPlanRunner{project: project}

	if !r.IsTerminal("integrated") {
		t.Errorf("integrated plan must be terminal")
	}
	if !r.IsTerminal("completed-no-merge-record") {
		t.Errorf("completed plan with no merge record is integrated (legacy semantics) — must be terminal")
	}
	if r.IsTerminal("completed-not-integrated") {
		t.Errorf("completed-but-not-integrated plan must NOT be terminal (needs integration re-entry)")
	}
	if r.IsTerminal("completed-cleanup-missing") {
		t.Errorf("merge-succeeded-but-cleanup-unrecorded plan must NOT be terminal")
	}
	if r.IsTerminal("running") {
		t.Errorf("running plan must not be terminal")
	}
	if r.IsTerminal("unknown") {
		t.Errorf("unknown plan must not be terminal")
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

	_, err := runBatchWithContext(ctx, root, run, b, io.Discard, "", 0, false, "")
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
	result, err := runBatchWithContext(ctx, root, run, b, io.Discard, "", 0, false, "")
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

// TestRunBatchWithContextMalformedLocalTOMLFails verifies that a present-but-
// malformed springfield.local.toml is surfaced as a wrapped error from
// runBatchWithContext, so operators see a clear "load springfield.local.toml"
// prefix instead of a bare TOML decode error.
func TestRunBatchWithContextMalformedLocalTOMLFails(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"),
		0o644,
	); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "springfield.local.toml"),
		[]byte("[review\nenabled = true\n"),
		0o644,
	); err != nil {
		t.Fatalf("write local toml: %v", err)
	}

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

	_, err := runBatchWithContext(context.Background(), root, run, b, io.Discard, "", 0, false, "")
	if err == nil {
		t.Fatal("expected error from malformed local toml, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("load springfield.local.toml")) {
		t.Errorf("error %q missing wrap prefix \"load springfield.local.toml\"", err.Error())
	}
}

