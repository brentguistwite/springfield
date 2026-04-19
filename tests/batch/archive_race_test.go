package batch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/batch"
)

// TestArchiveBatchNormalizedRetriesEmptyStableFile simulates the loser side
// of a writeJSONExclusive race: the stable archive path exists but has not
// yet been renamed into place (zero bytes). The loser entering
// maybeWriteArchiveSibling must retry reading instead of silently dropping
// the sibling. When the winner's bytes land mid-retry, the sibling should
// be written (reasons differ).
func TestArchiveBatchNormalizedRetriesEmptyStableFile(t *testing.T) {
	dir := t.TempDir()
	b := makeBatchForArchive("race-retry-1")

	// Ensure archive dir exists, then create the stable archive file empty
	// to simulate the winner having just O_EXCL-created it but not yet
	// finalized the rename.
	archiveDir := batch.ArchiveDir(dir)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	stablePath := batch.StableArchivePath(dir, b.ID)
	if err := os.WriteFile(stablePath, nil, 0o644); err != nil {
		t.Fatalf("seed empty stable: %v", err)
	}

	// In a background goroutine, finalize the stable file mid-retry with a
	// DIFFERENT reason ("replaced") so the loser's "state-tampered" produces
	// a sibling.
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(100 * time.Millisecond)
		finalized := batch.ArchiveEntry{
			BatchID: b.ID,
			Title:   b.Title,
			Reason:  "replaced",
		}
		data, _ := json.MarshalIndent(finalized, "", "  ")
		data = append(data, '\n')
		_ = os.WriteFile(stablePath, data, 0o644)
	}()

	// Seed a plan dir so ArchiveBatchNormalized has something to remove.
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

	if err := batch.ArchiveBatchNormalized(dir, b, "state-tampered"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	<-done

	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}

	var sibling os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if name == b.ID+".json" {
			continue
		}
		if strings.HasPrefix(name, b.ID+".") && strings.HasSuffix(name, ".json") {
			sibling = e
		}
	}
	if sibling == nil {
		t.Fatalf("expected sibling archive preserving state-tampered reason, got %v", names(entries))
	}

	data, err := os.ReadFile(filepath.Join(archiveDir, sibling.Name()))
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
}

// TestArchiveBatchNormalizedWritesCollisionSiblingOnPersistentStaleRead —
// the stable file stays empty for the entire retry budget. Rather than
// silently dropping the loser's reason, a sibling must be written with a
// `collision` marker so the distinct reason survives.
func TestArchiveBatchNormalizedWritesCollisionSiblingOnPersistentStaleRead(t *testing.T) {
	dir := t.TempDir()
	b := makeBatchForArchive("race-stale-1")

	archiveDir := batch.ArchiveDir(dir)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	stablePath := batch.StableArchivePath(dir, b.ID)
	if err := os.WriteFile(stablePath, nil, 0o644); err != nil {
		t.Fatalf("seed empty stable: %v", err)
	}

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

	if err := batch.ArchiveBatchNormalized(dir, b, "state-tampered"); err != nil {
		t.Fatalf("archive: %v", err)
	}

	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}

	var sibling os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if name == b.ID+".json" {
			continue
		}
		if strings.HasPrefix(name, b.ID+".") && strings.HasSuffix(name, ".json") {
			sibling = e
		}
	}
	if sibling == nil {
		t.Fatalf("expected sibling archive after exhausted retries, got %v", names(entries))
	}
	// Filename carries a collision marker so operators can spot races.
	if !strings.Contains(sibling.Name(), "collision") {
		t.Errorf("expected 'collision' in sibling name, got %q", sibling.Name())
	}
}
