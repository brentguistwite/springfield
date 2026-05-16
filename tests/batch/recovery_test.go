package batch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

// TestRecoverOrphanWritesStubAndClearsRun covers the primary incident path:
// run.json points at a batch id with no live plan dir. RecoverOrphan writes a
// stub archive with reason "orphaned" and clears run.json.
// Verifies orphan recovery still works against the new layout (Phase 2: Run
// now uses ActivePlanIDs instead of ActiveSliceIDs; batch has PlanIDs not Slices).
func TestRecoverOrphanWritesStubAndClearsRun(t *testing.T) {
	dir := t.TempDir()
	run := batch.Run{ActiveBatchID: "ghost", FatalError: "something bad"}
	if err := batch.WriteRun(dir, run); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	if err := batch.RecoverOrphan(dir, run); err != nil {
		t.Fatalf("RecoverOrphan: %v", err)
	}

	_, ok, _ := batch.ReadRun(dir)
	if ok {
		t.Error("run.json should be cleared after RecoverOrphan")
	}

	entries, _ := os.ReadDir(batch.ArchiveDir(dir))
	if len(entries) != 1 {
		t.Fatalf("archive entries = %d, want 1", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(batch.ArchiveDir(dir), entries[0].Name()))
	if !strings.Contains(string(data), `"reason": "orphaned"`) {
		t.Errorf("expected reason=orphaned in archive entry, got:\n%s", string(data))
	}
	if !strings.Contains(string(data), `"batch_id": "ghost"`) {
		t.Errorf("expected batch_id=ghost in archive entry, got:\n%s", string(data))
	}
}

// TestRecoverOrphanIdempotent — calling RecoverOrphan twice must not create a
// duplicate archive entry. This is load-bearing for the success-path crash
// scenario (archive already present, next start re-runs recover).
func TestRecoverOrphanIdempotent(t *testing.T) {
	dir := t.TempDir()
	run := batch.Run{ActiveBatchID: "ghost"}

	if err := batch.RecoverOrphan(dir, run); err != nil {
		t.Fatalf("first RecoverOrphan: %v", err)
	}
	// Put run.json back to simulate the "archive landed but ClearRun raced" state.
	if err := batch.WriteRun(dir, run); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	if err := batch.RecoverOrphan(dir, run); err != nil {
		t.Fatalf("second RecoverOrphan: %v", err)
	}

	entries, _ := os.ReadDir(batch.ArchiveDir(dir))
	if len(entries) != 1 {
		t.Errorf("archive entries = %d, want 1 (idempotent)", len(entries))
	}
	if _, ok, _ := batch.ReadRun(dir); ok {
		t.Error("run.json should be cleared after second RecoverOrphan")
	}
}

// TestIsMissingBatchErrorWorksThroughWrap — the detection predicate used by
// start.go relies on errors.Is unwrapping through ReadBatch's fmt.Errorf %w
// chain.
func TestIsMissingBatchErrorWorksThroughWrap(t *testing.T) {
	dir := t.TempDir()
	paths, _ := batch.NewPaths(dir, "nope")
	_, err := batch.ReadBatch(paths)
	if err == nil {
		t.Fatal("expected error reading missing batch")
	}
	if !batch.IsMissingBatchError(err) {
		t.Errorf("IsMissingBatchError(%q) = false, want true", err)
	}
}

// TestArchiveConcurrentWritersLandExactlyOnce simulates two near-simultaneous
// archive attempts and verifies the O_EXCL + stable path contract holds: only
// one archive file lives on disk for a given batch id.
func TestArchiveConcurrentWritersLandExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	b := batch.Batch{ID: "b", Title: "B", PlanIDs: []string{"01"}}
	paths, _ := batch.NewPaths(dir, "b")
	if err := batch.WriteBatch(paths, b, "", nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	const n = 8
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			errs <- batch.ArchiveBatchNormalized(dir, b, "completed", nil)
		}()
	}
	close(start)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("archive attempt %d: %v", i, err)
		}
	}

	entries, _ := os.ReadDir(batch.ArchiveDir(dir))
	if len(entries) != 1 {
		t.Fatalf("archive entries = %d, want 1 under concurrent writers", len(entries))
	}
	if entries[0].Name() != "b.json" {
		t.Errorf("archive filename = %q, want stable name b.json", entries[0].Name())
	}
}

// TestArchiveBatchIsIdempotentWhenArchiveAlreadyExists — if an archive for the
// batch id already exists, a second ArchiveBatchNormalized call does NOT write
// a duplicate entry. This is the success-path crash recovery guarantee.
func TestArchiveBatchIsIdempotentWhenArchiveAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	paths, _ := batch.NewPaths(dir, "b")
	b := batch.Batch{
		ID:      "b",
		Title:   "B",
		PlanIDs: []string{"01"},
	}
	if err := batch.WriteBatch(paths, b, "", nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := batch.ArchiveBatchNormalized(dir, b, "completed", nil); err != nil {
		t.Fatalf("first archive: %v", err)
	}

	// Re-create live state (as if crash restored it) and archive again.
	if err := batch.WriteBatch(paths, b, "", nil); err != nil {
		t.Fatalf("WriteBatch second: %v", err)
	}
	if err := batch.ArchiveBatchNormalized(dir, b, "completed", nil); err != nil {
		t.Fatalf("second archive: %v", err)
	}

	entries, _ := os.ReadDir(batch.ArchiveDir(dir))
	if len(entries) != 1 {
		t.Errorf("archive entries = %d, want 1 (idempotent)", len(entries))
	}
	if _, err := os.Stat(paths.PlanDir()); !os.IsNotExist(err) {
		t.Error("plan dir should be removed after second archive too")
	}
}
