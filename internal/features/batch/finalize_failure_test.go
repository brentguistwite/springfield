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

// TestFinalizeBatchRelocateFailureStillClearsCursor proves the "only ClearRun
// is fatal" contract for a sub-step INSIDE the archiveOK block: when evidence
// relocation fails (here a file occupies the archive/<batchID> path so the dst
// dir cannot be created), FinalizeBatch warns, leaves the evidence in place, and
// STILL clears the run cursor. A regression that moved ClearRun inside a
// sub-step's error arm would strand the cursor and be caught here.
func TestFinalizeBatchRelocateFailureStillClearsCursor(t *testing.T) {
	root, project, b := finalizeFixture(t)
	archiveDir := filepath.Join(root, ".springfield", "archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	// Occupy the per-batch archive dir path with a FILE: the entry write targets
	// archive/batch-1.json (succeeds, archiveOK=true), but relocation's
	// MkdirAll(archive/batch-1/plans) then fails ("not a directory").
	if err := os.WriteFile(filepath.Join(archiveDir, "batch-1"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed relocate collision: %v", err)
	}
	var warns bytes.Buffer

	if err := batch.FinalizeBatch(root, b, project, &cost.Rollup{}, "per-plan", &warns); err != nil {
		t.Fatalf("FinalizeBatch must not hard-fail on a relocate failure: %v", err)
	}

	if !strings.Contains(warns.String(), "relocate evidence") {
		t.Fatalf("expected a relocate warning, got %q", warns.String())
	}
	// Evidence preserved in place because the relocate failed.
	if _, err := os.Stat(filepath.Join(root, ".springfield", "execution", "plans", "alpha", "iter-1", "cost.json")); err != nil {
		t.Fatalf("evidence must stay in execution/ when relocate fails: %v", err)
	}
	// The only fatal step still ran: run cursor cleared.
	if _, ok, _ := batch.ReadRun(root); ok {
		t.Fatal("run cursor must be cleared even when a best-effort sub-step fails")
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
