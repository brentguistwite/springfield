package batch_test

import (
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/cost"
)

func TestLatestArchiveReturnsMostRecentByArchivedAt(t *testing.T) {
	root := t.TempDir()

	// Two archives; batch-2 archived later.
	older := batch.Batch{ID: "batch-1", Title: "First", PlanIDs: []string{"a"}}
	newer := batch.Batch{ID: "batch-2", Title: "Second", PlanIDs: []string{"b"}}
	if err := batch.ArchiveBatchNormalized(root, older, "completed", &cost.Rollup{}); err != nil {
		t.Fatalf("archive older: %v", err)
	}
	// Ensure a distinct, later ArchivedAt by writing the second after a beat.
	time.Sleep(10 * time.Millisecond)
	if err := batch.ArchiveBatchNormalized(root, newer, "completed", &cost.Rollup{}); err != nil {
		t.Fatalf("archive newer: %v", err)
	}

	entry, ok, err := batch.LatestArchive(root)
	if err != nil {
		t.Fatalf("LatestArchive: %v", err)
	}
	if !ok {
		t.Fatal("expected an archive entry")
	}
	if entry.BatchID != "batch-2" {
		t.Fatalf("latest = %q, want batch-2", entry.BatchID)
	}
}

// The mode-carrying variant (used by the completion fallback when the project
// can't be loaded) must stamp BatchMode onto the entry, so a per-plan batch
// archived via the fallback still projects as retained, not merged. The plain
// ArchiveBatchNormalized keeps the empty-mode behavior for reap/replace/orphan.
func TestArchiveBatchNormalizedWithModeStampsMode(t *testing.T) {
	root := t.TempDir()
	b := batch.Batch{ID: "batch-1", Title: "T", PlanIDs: []string{"a"}}
	if err := batch.ArchiveBatchNormalizedWithMode(root, b, "completed", &cost.Rollup{}, "per-plan"); err != nil {
		t.Fatalf("archive with mode: %v", err)
	}
	entry, ok, err := batch.LatestArchive(root)
	if err != nil || !ok {
		t.Fatalf("LatestArchive: ok=%v err=%v", ok, err)
	}
	if entry.BatchMode != "per-plan" {
		t.Fatalf("BatchMode = %q, want per-plan", entry.BatchMode)
	}
}

func TestArchiveBatchNormalizedDefaultModeEmpty(t *testing.T) {
	root := t.TempDir()
	b := batch.Batch{ID: "batch-1", Title: "T", PlanIDs: []string{"a"}}
	if err := batch.ArchiveBatchNormalized(root, b, "replaced", &cost.Rollup{}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	entry, _, _ := batch.LatestArchive(root)
	if entry.BatchMode != "" {
		t.Fatalf("BatchMode = %q, want empty for the reap/replace path", entry.BatchMode)
	}
}

func TestLatestArchiveEmptyWhenNoArchives(t *testing.T) {
	root := t.TempDir()
	_, ok, err := batch.LatestArchive(root)
	if err != nil {
		t.Fatalf("LatestArchive on empty: %v", err)
	}
	if ok {
		t.Fatal("expected no archive entry on a fresh repo")
	}
}
