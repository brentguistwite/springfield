package batch_test

import (
	"os"
	"path/filepath"
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
