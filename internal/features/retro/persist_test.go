package retro_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/retro"
)

// writeFixtureArchive lays down a minimal finished-batch archive dir: an
// archive.json header naming one plan plus that plan's relocated evidence tail
// (summary.json) under plans/<key>/. exitReason drives which classifier trips.
func writeFixtureArchive(t *testing.T, batchDir, planID, exitReason string) {
	t.Helper()
	entry := map[string]any{
		"batch_id":   filepath.Base(batchDir),
		"title":      "fixture batch",
		"batch_mode": "consolidate",
		"plans": []map[string]any{
			{"id": planID, "title": "a plan", "status": "failed",
				"evidence_path": filepath.Join(".springfield", "archive", filepath.Base(batchDir), "plans", planID)},
		},
	}
	data, _ := json.MarshalIndent(entry, "", "  ")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatalf("mkdir batchDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(batchDir, "archive.json"), data, 0o644); err != nil {
		t.Fatalf("write archive.json: %v", err)
	}
	evDir := filepath.Join(batchDir, "plans", planID)
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	sum, _ := json.Marshal(map[string]any{
		"iteration_count": 5, "terminal_status": "failed", "exit_reason": exitReason,
	})
	if err := os.WriteFile(filepath.Join(evDir, "summary.json"), sum, 0o644); err != nil {
		t.Fatalf("write summary.json: %v", err)
	}
}

// TestPersist_ExtractsClassifiesWrites pins the completion-path composition:
// Persist extracts the report, runs the classifiers over it, and writes a
// retro.json carrying the resulting findings beside the archive.
func TestPersist_ExtractsClassifiesWrites(t *testing.T) {
	batchDir := filepath.Join(t.TempDir(), "b1")
	writeFixtureArchive(t, batchDir, "US-001", "iteration cap reached without completion marker")

	report, err := retro.Persist(batchDir)
	if err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if report == nil {
		t.Fatal("Persist returned nil report")
	}
	if len(report.Findings) != 1 || report.Findings[0].PatternKey != "iteration-cap" {
		t.Fatalf("expected one iteration-cap finding, got %+v", report.Findings)
	}

	// The report must be durable on disk with the findings included.
	raw, err := os.ReadFile(filepath.Join(batchDir, "retro.json"))
	if err != nil {
		t.Fatalf("read retro.json: %v", err)
	}
	var onDisk retro.Report
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("decode retro.json: %v", err)
	}
	if len(onDisk.Findings) != 1 || onDisk.Findings[0].PatternKey != "iteration-cap" {
		t.Fatalf("persisted findings mismatch: %+v", onDisk.Findings)
	}
	if onDisk.BatchID != "b1" {
		t.Errorf("persisted batch id = %q, want b1", onDisk.BatchID)
	}
}

// TestPersist_EmptyBatchDirErrors confirms the one caller-mistake error path
// (empty batchDir) surfaces rather than silently writing nothing.
func TestPersist_EmptyBatchDirErrors(t *testing.T) {
	if _, err := retro.Persist(""); err == nil {
		t.Fatal("expected error for empty batchDir")
	}
}

// TestPersist_WriteFailureReturnsReport proves a WriteReport failure is
// reported as an error while the extracted report is still returned — the raw
// material the warning-only caller may inspect. A retro.json that already exists
// as a directory makes the atomic rename fail deterministically.
func TestPersist_WriteFailureReturnsReport(t *testing.T) {
	batchDir := filepath.Join(t.TempDir(), "b1")
	writeFixtureArchive(t, batchDir, "US-001", "iteration cap reached without completion marker")
	if err := os.MkdirAll(filepath.Join(batchDir, "retro.json"), 0o755); err != nil {
		t.Fatalf("mkdir retro.json dir: %v", err)
	}

	report, err := retro.Persist(batchDir)
	if err == nil {
		t.Fatal("expected write failure error")
	}
	if report == nil {
		t.Fatal("expected the extracted report to be returned alongside the write error")
	}
}
