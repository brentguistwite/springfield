package batch_test

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/cost"
)

// TestLatestArchiveSkipsTamperSidecars proves LatestArchive ignores tamper
// forensics sidecars (<id>.<nanos>.tamper.json) — they share the archive dir
// but have no archived_at, so without the skip a newer-mod-time sidecar could
// win the fallback and surface a misleading "archived" view.
func TestLatestArchiveSkipsTamperSidecars(t *testing.T) {
	root := t.TempDir()
	real := batch.Batch{ID: "batch-1", Title: "Real Batch", PlanIDs: []string{"a"}}
	if err := batch.ArchiveBatchNormalized(root, real, "completed", &cost.Rollup{}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// A tamper sidecar with a far-future detected_at and no archived_at, written
	// AFTER the real archive (so its mod-time is newer).
	sidecar := filepath.Join(root, ".springfield", "archive", "batch-1.9999999999999999.tamper.json")
	if err := os.WriteFile(sidecar, []byte(`{"batch_id":"batch-1","reason":"control-plane-tamper","detected_at":"2099-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	entry, ok, err := batch.LatestArchive(root)
	if err != nil {
		t.Fatalf("LatestArchive: %v", err)
	}
	if !ok {
		t.Fatal("expected the real archive entry")
	}
	if entry.Title != "Real Batch" {
		t.Fatalf("tamper sidecar must be skipped; got entry %+v", entry)
	}
}
