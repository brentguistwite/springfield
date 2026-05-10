package batch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

// TestWriteBatchRollsBackBatchDirOnPerPlanWriteFailure verifies that when a
// per-plan write fails AND the batch dir was newly created, WriteBatch removes
// the entire batch dir (including batch.json and source.md) so no partial state
// is left on disk.
func TestWriteBatchRollsBackBatchDirOnPerPlanWriteFailure(t *testing.T) {
	root := t.TempDir()

	b := batch.Batch{
		ID:      "test-batch",
		Title:   "Test Batch",
		PlanIDs: []string{"plan-one", "plan-two"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-one", "plan-two"}}},
	}
	paths, err := batch.NewPaths(root, b.ID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	// Create the plans parent dir, then make it read-only so that after
	// plan-one succeeds, the RemoveAll+MkdirAll for plan-two fails (the
	// parent dir permission blocks creating plan-two).
	plansParent := filepath.Join(root, ".springfield", "plans")
	if err := os.MkdirAll(plansParent, 0o755); err != nil {
		t.Fatalf("mkdir plans parent: %v", err)
	}

	plans := []batch.WrittenPlan{
		{
			ID:       "plan-one",
			PRDBytes: []byte(`{"id":"plan-one"}`),
		},
		{
			ID:       "plan-two",
			PRDBytes: []byte(`{"id":"plan-two"}`),
		},
	}

	// Make the plans parent read-only before WriteBatch runs. This blocks
	// MkdirAll for plan-two (and RemoveAll won't help since the parent itself
	// is read-only, preventing dir removal).
	if err := os.Chmod(plansParent, 0o555); err != nil {
		t.Fatalf("chmod plans parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(plansParent, 0o755) })

	err = batch.WriteBatch(paths, b, "source content", plans)
	// Restore permissions before assertions so t.TempDir cleanup works.
	_ = os.Chmod(plansParent, 0o755)
	if err == nil {
		t.Fatal("expected WriteBatch to fail when per-plan write fails")
	}

	// The batch dir must NOT exist (rolled back).
	batchDir := filepath.Join(root, ".springfield", "plans", "test-batch")
	if _, statErr := os.Stat(batchDir); statErr == nil {
		t.Errorf("batch dir %q still exists after failed write — rollback incomplete", batchDir)
	}
}

// TestWriteBatchClearsStalePerPlanFilesOnReplace verifies that when WriteBatch
// is called for a plan ID whose directory already exists (--replace reusing a
// plan ID), any files not written by the new envelope (e.g. stale context.md
// and progress.md from the prior batch) are removed. Only the new prd.json
// must be present; the old context.md and progress.md must be gone.
func TestWriteBatchClearsStalePerPlanFilesOnReplace(t *testing.T) {
	root := t.TempDir()

	// Pre-populate the plan dir with stale files from a prior batch.
	planDir := filepath.Join(root, ".springfield", "plans", "plan-one")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	staleCtx := filepath.Join(planDir, "context.md")
	staleProgress := filepath.Join(planDir, "progress.md")
	if err := os.WriteFile(staleCtx, []byte("old context"), 0o644); err != nil {
		t.Fatalf("write stale context.md: %v", err)
	}
	if err := os.WriteFile(staleProgress, []byte("old progress"), 0o644); err != nil {
		t.Fatalf("write stale progress.md: %v", err)
	}

	b := batch.Batch{
		ID:      "test-batch",
		Title:   "Test Batch",
		PlanIDs: []string{"plan-one"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-one"}}},
	}
	paths, err := batch.NewPaths(root, b.ID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	// New envelope omits context_md — simulating --replace with no context.
	plans := []batch.WrittenPlan{
		{
			ID:           "plan-one",
			PRDBytes:     []byte(`{"id":"plan-one","title":"Plan One"}`),
			ContextBytes: nil, // intentionally absent
		},
	}

	if err := batch.WriteBatch(paths, b, "source content", plans); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	// Stale files must be gone.
	if _, err := os.Stat(staleCtx); err == nil {
		t.Errorf("stale context.md still exists after WriteBatch with --replace")
	}
	if _, err := os.Stat(staleProgress); err == nil {
		t.Errorf("stale progress.md still exists after WriteBatch with --replace")
	}

	// New prd.json must exist.
	if _, err := os.Stat(filepath.Join(planDir, "prd.json")); err != nil {
		t.Errorf("new prd.json must exist: %v", err)
	}
}

// TestWriteBatchAppendRollsBackBatchJSONOnPerPlanFailure verifies that when an
// --append WriteBatch fails during per-plan writes, the prior batch.json is
// restored to its original content and only the newly-created plan dirs are
// removed (pre-existing plan dirs are untouched).
func TestWriteBatchAppendRollsBackBatchJSONOnPerPlanFailure(t *testing.T) {
	root := t.TempDir()

	// Pre-create the batch dir with an original batch.json (simulating an
	// existing batch that will be appended to).
	batchDir := filepath.Join(root, ".springfield", "plans", "test-batch")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatalf("mkdir batch dir: %v", err)
	}

	originalBatch := batch.Batch{
		ID:      "test-batch",
		Title:   "Original",
		PlanIDs: []string{"plan-existing"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-existing"}}},
	}
	originalData, err := json.MarshalIndent(originalBatch, "", "  ")
	if err != nil {
		t.Fatalf("marshal original batch: %v", err)
	}
	originalData = append(originalData, '\n')
	batchJSONPath := filepath.Join(batchDir, "batch.json")
	if err := os.WriteFile(batchJSONPath, originalData, 0o644); err != nil {
		t.Fatalf("write original batch.json: %v", err)
	}

	// Pre-create plan-existing dir (it should remain untouched after rollback).
	existingPlanDir := filepath.Join(root, ".springfield", "plans", "plan-existing")
	if err := os.MkdirAll(existingPlanDir, 0o755); err != nil {
		t.Fatalf("mkdir existing plan dir: %v", err)
	}

	// New batch includes plan-existing + plan-new. plan-new write will fail
	// because we make the plans parent dir read-only.
	newBatch := batch.Batch{
		ID:      "test-batch",
		Title:   "Appended",
		PlanIDs: []string{"plan-existing", "plan-new"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-existing", "plan-new"}}},
	}
	paths, err := batch.NewPaths(root, "test-batch")
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	// Make the plans parent read-only to cause plan-new dir creation to fail.
	plansParent := filepath.Join(root, ".springfield", "plans")
	if err := os.Chmod(plansParent, 0o555); err != nil {
		t.Fatalf("chmod plans parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(plansParent, 0o755) })

	plans := []batch.WrittenPlan{
		// plan-existing is in PlanIDs but WriteBatch does RemoveAll+MkdirAll for
		// each plan in plans slice. We only pass plan-new to trigger the failure
		// on the new dir creation (the existing dir removal fails since parent is 0555).
		{
			ID:       "plan-new",
			PRDBytes: []byte(`{"id":"plan-new"}`),
		},
	}

	writeErr := batch.WriteBatch(paths, newBatch, "appended source", plans)
	_ = os.Chmod(plansParent, 0o755) // restore before assertions

	if writeErr == nil {
		t.Fatal("expected WriteBatch to fail when per-plan write fails")
	}

	// batch.json must be restored to original content.
	afterData, err := os.ReadFile(batchJSONPath)
	if err != nil {
		t.Fatalf("read batch.json after failed append: %v", err)
	}
	if string(afterData) != string(originalData) {
		t.Errorf("batch.json was not rolled back:\ngot:  %s\nwant: %s", afterData, originalData)
	}

	// plan-existing dir must still exist (pre-existing, not cleaned up).
	if _, statErr := os.Stat(existingPlanDir); statErr != nil {
		t.Errorf("pre-existing plan dir should survive rollback: %v", statErr)
	}

	// plan-new dir must NOT exist (new dir, cleaned up by rollback).
	newPlanDir := filepath.Join(root, ".springfield", "plans", "plan-new")
	if _, statErr := os.Stat(newPlanDir); statErr == nil {
		t.Errorf("new plan dir should be removed by rollback")
	}
}

// TestReadBatchRejectsLegacySlicesShape verifies that a batch.json written
// with the old phases[].slices field (pre-rename) is rejected with a clear
// error rather than silently unmarshalling with empty plans.
func TestReadBatchRejectsLegacySlicesShape(t *testing.T) {
	root := t.TempDir()

	// Build a legacy-shape batch: phases contain "slices" not "plans".
	legacyBatch := map[string]any{
		"id":    "legacy-batch",
		"title": "Legacy",
		"phases": []map[string]any{
			{
				"mode":   "serial",
				"slices": []string{"plan-a", "plan-b"},
			},
		},
		"plan_ids": []string{"plan-a", "plan-b"},
	}
	data, err := json.MarshalIndent(legacyBatch, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy batch: %v", err)
	}

	// Write into the expected batch path.
	batchDir := filepath.Join(root, ".springfield", "plans", "legacy-batch")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatalf("mkdir batch dir: %v", err)
	}
	batchPath := filepath.Join(batchDir, "batch.json")
	if err := os.WriteFile(batchPath, data, 0o644); err != nil {
		t.Fatalf("write batch.json: %v", err)
	}

	paths, err := batch.NewPaths(root, "legacy-batch")
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	_, readErr := batch.ReadBatch(paths)
	if readErr == nil {
		t.Fatal("expected ReadBatch to return error for legacy slices shape, got nil")
	}
	if !strings.Contains(readErr.Error(), "legacy batch shape") {
		t.Errorf("error should mention legacy batch shape, got: %v", readErr)
	}
}

// TestWriteBatchSuccessLeavesAllFilesIntact verifies the happy path: all files
// are created and the batch dir remains on disk.
func TestWriteBatchSuccessLeavesAllFilesIntact(t *testing.T) {
	root := t.TempDir()

	b := batch.Batch{
		ID:      "test-batch",
		Title:   "Test Batch",
		PlanIDs: []string{"plan-one"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-one"}}},
	}
	paths, err := batch.NewPaths(root, b.ID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	plans := []batch.WrittenPlan{
		{
			ID:       "plan-one",
			PRDBytes: []byte(`{"id":"plan-one","title":"Plan One"}`),
		},
	}

	if err := batch.WriteBatch(paths, b, "source content", plans); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	// batch.json, source.md, and plan prd.json must all exist.
	for _, name := range []string{
		filepath.Join(root, ".springfield", "plans", "test-batch", "batch.json"),
		filepath.Join(root, ".springfield", "plans", "test-batch", "source.md"),
		filepath.Join(root, ".springfield", "plans", "plan-one", "prd.json"),
	} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
		}
	}
}
