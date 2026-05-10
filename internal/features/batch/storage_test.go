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

	// Pre-create plan-two's dir as a FILE (not dir) so MkdirAll fails on it,
	// forcing a write failure mid-loop after plan-one has been created.
	planTwoParent := filepath.Join(root, ".springfield", "plans", "plan-two")
	if err := os.MkdirAll(filepath.Dir(planTwoParent), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(planTwoParent, []byte("block"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
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

	err = batch.WriteBatch(paths, b, "source content", plans)
	if err == nil {
		t.Fatal("expected WriteBatch to fail when per-plan write fails")
	}

	// The batch dir must NOT exist (rolled back).
	batchDir := filepath.Join(root, ".springfield", "plans", "test-batch")
	if _, statErr := os.Stat(batchDir); statErr == nil {
		t.Errorf("batch dir %q still exists after failed write — rollback incomplete", batchDir)
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
