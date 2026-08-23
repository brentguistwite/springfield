package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRetroArchiveFixture lays down a finished-batch archive dir at
// .springfield/archive/<batchID>/ with an archive.json header and one plan's
// summary.json evidence tail, so emitBatchRetro has something real to extract.
func writeRetroArchiveFixture(t *testing.T, root, batchID string) string {
	t.Helper()
	batchDir := filepath.Join(root, ".springfield", "archive", batchID)
	evDir := filepath.Join(batchDir, "plans", "US-001")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	entry := map[string]any{
		"batch_id":   batchID,
		"title":      "fixture",
		"batch_mode": "consolidate",
		"plans": []map[string]any{
			{"id": "US-001", "title": "a plan", "status": "failed",
				"evidence_path": filepath.Join(".springfield", "archive", batchID, "plans", "US-001")},
		},
	}
	data, _ := json.MarshalIndent(entry, "", "  ")
	if err := os.WriteFile(filepath.Join(batchDir, "archive.json"), data, 0o644); err != nil {
		t.Fatalf("write archive.json: %v", err)
	}
	sum, _ := json.Marshal(map[string]any{
		"iteration_count": 5, "terminal_status": "failed",
		"exit_reason": "iteration cap reached without completion marker",
	})
	if err := os.WriteFile(filepath.Join(evDir, "summary.json"), sum, 0o644); err != nil {
		t.Fatalf("write summary.json: %v", err)
	}
	return batchDir
}

// TestEmitBatchRetro_Success confirms the happy path writes retro.json beside
// the archive with classified findings and emits nothing on the warning stream.
func TestEmitBatchRetro_Success(t *testing.T) {
	root := t.TempDir()
	batchDir := writeRetroArchiveFixture(t, root, "batch-1")

	var buf bytes.Buffer
	emitBatchRetro(&buf, root, "batch-1")

	if buf.Len() != 0 {
		t.Errorf("expected no warning on success, got %q", buf.String())
	}
	raw, err := os.ReadFile(filepath.Join(batchDir, "retro.json"))
	if err != nil {
		t.Fatalf("retro.json not written: %v", err)
	}
	var report struct {
		Findings []struct {
			PatternKey string `json:"pattern_key"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode retro.json: %v", err)
	}
	if len(report.Findings) != 1 || report.Findings[0].PatternKey != "iteration-cap" {
		t.Fatalf("expected one iteration-cap finding, got %+v", report.Findings)
	}
}

// TestEmitBatchRetro_WriteFailureWarnsNotFatal is the focused warn-not-fail
// proof: when retro.json cannot be written (it already exists as a directory,
// so the atomic rename fails), emitBatchRetro emits a grep-friendly
// "warning: retro:" line and returns normally — it never panics or exits, so
// the batch outcome and exit code are unaffected.
func TestEmitBatchRetro_WriteFailureWarnsNotFatal(t *testing.T) {
	root := t.TempDir()
	batchDir := writeRetroArchiveFixture(t, root, "batch-1")
	if err := os.MkdirAll(filepath.Join(batchDir, "retro.json"), 0o755); err != nil {
		t.Fatalf("mkdir retro.json dir: %v", err)
	}

	var buf bytes.Buffer
	emitBatchRetro(&buf, root, "batch-1")

	out := buf.String()
	if !strings.Contains(out, "warning: retro:") {
		t.Errorf("expected grep-friendly warning, got %q", out)
	}
	if !strings.Contains(out, "batch-1") {
		t.Errorf("expected batch id in warning, got %q", out)
	}
}
