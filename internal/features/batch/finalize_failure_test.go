package batch_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
)

// TestFinalizeBatchArchiveFailurePreservesEvidenceAndUnits proves the
// archive-write-first ordering: when the archive entry cannot be written,
// evidence is NOT relocated (stays discoverable in execution/), the batch's
// units are NOT deregistered, the run cursor is still cleared, and a warning
// is surfaced. Closes the orphaned-evidence regression.
func TestFinalizeBatchArchiveFailurePreservesEvidenceAndUnits(t *testing.T) {
	root, project, b := finalizeFixture(t)
	// Force the archive write to fail by occupying the archive dir path with a file.
	if err := os.WriteFile(filepath.Join(root, ".springfield", "archive"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("seed archive collision: %v", err)
	}
	var warns bytes.Buffer

	if err := batch.FinalizeBatch(root, b, project, &cost.Rollup{}, "per-plan", &warns); err != nil {
		t.Fatalf("FinalizeBatch must not hard-fail on archive failure: %v", err)
	}

	// Evidence preserved in place (NOT relocated to the failed archive namespace).
	if _, err := os.Stat(filepath.Join(root, ".springfield", "execution", "plans", "alpha", "iter-1", "cost.json")); err != nil {
		t.Fatalf("evidence must stay in execution/ on archive failure: %v", err)
	}
	// Units preserved (not deregistered) so the batch stays recoverable.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	found := false
	for _, u := range reloaded.Config.PlanUnits {
		if u.ID == "alpha" {
			found = true
		}
	}
	if !found {
		t.Fatal("batch units must survive an archive failure for recovery")
	}
	// Run cursor still cleared (the only fatal step).
	if _, ok, _ := batch.ReadRun(root); ok {
		t.Fatal("run cursor must be cleared even on archive failure")
	}
	if !strings.Contains(warns.String(), "warning: archive") {
		t.Fatalf("expected an archive warning, got %q", warns.String())
	}
}

// TestFinalizeBatchRecordsEvidencePathWhenAlreadyRelocated proves the relocate
// idempotency + entry-first ordering: if a crash already moved evidence to the
// archive namespace (src gone), FinalizeBatch still records a non-empty
// evidence_path rather than lying with "".
func TestFinalizeBatchRecordsEvidencePathWhenAlreadyRelocated(t *testing.T) {
	root, project, b := finalizeFixture(t)
	// Simulate a crash AFTER relocation: evidence already at the archive dst, src gone.
	src := filepath.Join(root, ".springfield", "execution", "plans", "alpha")
	dst := filepath.Join(root, ".springfield", "archive", "batch-1", "plans", "alpha")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir dst: %v", err)
	}
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("pre-relocate: %v", err)
	}

	if err := batch.FinalizeBatch(root, b, project, &cost.Rollup{}, "per-plan", io.Discard); err != nil {
		t.Fatalf("FinalizeBatch: %v", err)
	}

	var entry batch.ArchiveEntry
	data, err := os.ReadFile(batch.StableArchivePath(root, "batch-1"))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var alpha *batch.ArchivePlan
	for i := range entry.Plans {
		if entry.Plans[i].ID == "alpha" {
			alpha = &entry.Plans[i]
		}
	}
	if alpha == nil || alpha.EvidencePath == "" {
		t.Fatalf("evidence_path must be recorded even when evidence was already relocated: %+v", alpha)
	}
	if entry.BatchMode != "per-plan" {
		t.Fatalf("entry.BatchMode = %q, want per-plan", entry.BatchMode)
	}
}
