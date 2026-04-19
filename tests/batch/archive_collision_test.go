package batch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

func makeBatchForArchive(id string) batch.Batch {
	return batch.Batch{
		ID:    id,
		Title: "Archive collision test",
		Phases: []batch.Phase{
			{Slices: []string{"00"}},
		},
		Slices: []batch.Slice{
			{ID: "00", Title: "only", Status: batch.SliceQueued},
		},
	}
}

// TestArchiveBatchNormalizedWritesSiblingOnReasonCollision verifies that when
// a second archive lands for the same batch id but a different reason, a
// sibling file is written so no forensic info is lost.
func TestArchiveBatchNormalizedWritesSiblingOnReasonCollision(t *testing.T) {
	dir := t.TempDir()
	b := makeBatchForArchive("coll-1")

	// Seed plan dir (so cleanup runs do something).
	paths, err := batch.NewPaths(dir, b.ID)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	if err := os.WriteFile(paths.BatchPath(), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed batch.json: %v", err)
	}

	if err := batch.ArchiveBatchNormalized(dir, b, "replaced"); err != nil {
		t.Fatalf("archive 1: %v", err)
	}
	// Re-seed plan dir for the second archive (first call removed it).
	if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
		t.Fatalf("recreate plan dir: %v", err)
	}
	if err := os.WriteFile(paths.BatchPath(), []byte("{}"), 0o644); err != nil {
		t.Fatalf("reseed batch.json: %v", err)
	}
	if err := batch.ArchiveBatchNormalized(dir, b, "state-tampered"); err != nil {
		t.Fatalf("archive 2: %v", err)
	}

	entries, err := os.ReadDir(batch.ArchiveDir(dir))
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}

	var stable, sibling os.DirEntry
	for _, e := range entries {
		name := e.Name()
		switch {
		case name == b.ID+".json":
			stable = e
		case strings.HasPrefix(name, b.ID+".") && strings.HasSuffix(name, ".json"):
			sibling = e
		}
	}
	if stable == nil {
		t.Fatalf("expected stable archive %s.json, entries=%v", b.ID, names(entries))
	}
	if sibling == nil {
		t.Fatalf("expected sibling archive for second reason, entries=%v", names(entries))
	}

	// Sibling must carry the second reason.
	data, err := os.ReadFile(filepath.Join(batch.ArchiveDir(dir), sibling.Name()))
	if err != nil {
		t.Fatalf("read sibling: %v", err)
	}
	var entry batch.ArchiveEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("decode sibling: %v", err)
	}
	if entry.Reason != "state-tampered" {
		t.Errorf("sibling reason = %q, want state-tampered", entry.Reason)
	}

	// Filename must include the reason for operability.
	if !strings.Contains(sibling.Name(), "state-tampered") {
		t.Errorf("sibling name should embed reason, got %q", sibling.Name())
	}
}

// TestArchiveBatchNormalizedIdempotentSameReason verifies the current
// idempotent behavior: same reason arriving twice yields a single archive
// file, no sibling.
func TestArchiveBatchNormalizedIdempotentSameReason(t *testing.T) {
	dir := t.TempDir()
	b := makeBatchForArchive("idemp-1")

	paths, err := batch.NewPaths(dir, b.ID)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
			t.Fatalf("mkdir plan dir: %v", err)
		}
		if err := os.WriteFile(paths.BatchPath(), []byte("{}"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := batch.ArchiveBatchNormalized(dir, b, "completed"); err != nil {
			t.Fatalf("archive %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(batch.ArchiveDir(dir))
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 archive entry for same reason, got %d: %v", len(entries), names(entries))
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
