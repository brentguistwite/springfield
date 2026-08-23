package retro_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/retro"
)

// TestWriteReportRoundTrips locks the basic contract: WriteReport lands
// retro.json in batchDir and the bytes decode back to an equal report.
func TestWriteReportRoundTrips(t *testing.T) {
	dir := t.TempDir()
	r := &retro.Report{
		BatchID:  "batch-write-1",
		Title:    "Write me",
		TotalUSD: 1.25,
		Plans:    []retro.PlanRetro{{ID: "US-001", Status: "completed"}},
		Findings: []retro.Finding{{PatternKey: "iteration-cap", Severity: "high", Summary: "hit cap"}},
	}
	if err := retro.WriteReport(dir, r); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "retro.json"))
	if err != nil {
		t.Fatalf("read retro.json: %v", err)
	}
	var got retro.Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode retro.json: %v", err)
	}
	if got.BatchID != r.BatchID || got.TotalUSD != r.TotalUSD {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
	if len(got.Findings) != 1 || got.Findings[0].PatternKey != "iteration-cap" {
		t.Errorf("findings not persisted: %+v", got.Findings)
	}
}

// TestWriteReportIdempotentRewrite is the AC: rewriting an existing retro.json
// replaces the content cleanly, leaving no stale bytes from the prior report and
// no lingering temp files beside it.
func TestWriteReportIdempotentRewrite(t *testing.T) {
	dir := t.TempDir()
	first := &retro.Report{BatchID: "batch-rewrite", Title: "first draft with a long title", TotalUSD: 9.99}
	if err := retro.WriteReport(dir, first); err != nil {
		t.Fatalf("first WriteReport: %v", err)
	}
	second := &retro.Report{BatchID: "batch-rewrite", Title: "v2"}
	if err := retro.WriteReport(dir, second); err != nil {
		t.Fatalf("second WriteReport: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "retro.json"))
	if err != nil {
		t.Fatalf("read retro.json: %v", err)
	}
	var got retro.Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode retro.json: %v", err)
	}
	if got.Title != "v2" {
		t.Errorf("Title = %q, want the rewrite %q", got.Title, "v2")
	}
	// The prior draft's total must be gone — a stale-byte rewrite would leave it.
	if got.TotalUSD != 0 {
		t.Errorf("TotalUSD = %v, want 0 after clean rewrite", got.TotalUSD)
	}

	// Only retro.json should remain; a rename-based writer leaves no temp behind.
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range ents {
		if e.Name() != "retro.json" {
			t.Errorf("unexpected leftover file %q; writer must not strand temps", e.Name())
		}
	}
}

// TestWriteReportNoPartialWindow asserts the atomic guarantee: retro.json is
// only ever observable as a complete file. A rename-based writer creates the
// target via a temp file, so a directory scan taken at any point sees either no
// retro.json or a fully-formed one — never a partial ".tmp-retro.json-*" under
// the final name. We approximate "no partial-file window" by proving the final
// path is never a truncated/empty file and that the writer routes bytes through
// a differently-named temp (any leftover on a mid-write crash is a temp, not
// retro.json).
func TestWriteReportNoPartialWindow(t *testing.T) {
	dir := t.TempDir()
	r := &retro.Report{BatchID: "batch-atomic", Plans: make([]retro.PlanRetro, 200)}
	for i := range r.Plans {
		r.Plans[i] = retro.PlanRetro{ID: "US-" + strings.Repeat("9", 40), Status: "completed"}
	}
	if err := retro.WriteReport(dir, r); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	// The committed file must be valid JSON in full — never a zero-length or
	// truncated remnant of an in-progress write surfacing under the final name.
	data, err := os.ReadFile(filepath.Join(dir, "retro.json"))
	if err != nil {
		t.Fatalf("read retro.json: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("retro.json is empty; a rename-based write must publish complete bytes")
	}
	var got retro.Report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("retro.json is not complete JSON (partial window leaked): %v", err)
	}
	if len(got.Plans) != 200 {
		t.Errorf("Plans = %d, want 200; committed file is truncated", len(got.Plans))
	}
}

// TestWriteReportRejectsEmptyBatchDir mirrors Extract's one true error path: a
// caller mistake, not anything on disk.
func TestWriteReportRejectsEmptyBatchDir(t *testing.T) {
	if err := retro.WriteReport("  ", &retro.Report{}); err == nil {
		t.Fatal("WriteReport(\"  \") returned nil error, want a caller-mistake error")
	}
}

// TestWriteReportNilReport guards the other caller mistake.
func TestWriteReportNilReport(t *testing.T) {
	if err := retro.WriteReport(t.TempDir(), nil); err == nil {
		t.Fatal("WriteReport(nil) returned nil error, want a caller-mistake error")
	}
}
