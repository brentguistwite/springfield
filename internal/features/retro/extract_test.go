package retro_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/retro"
)

// update regenerates the golden.json files from the current Extract output.
// Run: go test ./internal/features/retro -run TestExtractGolden -update
var update = flag.Bool("update", false, "regenerate golden files")

// TestExtractGolden pins Extract's whole-Report output for each finalized
// archive shape. The fixtures live beside this test under testdata/<case>/ and
// are self-contained batch archive dirs (archive.json + plans/<key>/ evidence),
// so the golden covers the header join, per-plan evidence tail, iteration
// ordering, attempt chains, verify rounds, and the degraded-notes path together.
func TestExtractGolden(t *testing.T) {
	cases := []string{"success", "failed-plan", "consolidate-shape", "missing-evidence", "corrupt-json"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			batchDir := filepath.Join("testdata", name)
			report, err := retro.Extract(batchDir)
			if err != nil {
				t.Fatalf("Extract(%s) returned error: %v", batchDir, err)
			}
			got, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				t.Fatalf("marshal report: %v", err)
			}
			got = append(got, '\n')

			goldenPath := filepath.Join(batchDir, "golden.json")
			if *update {
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("report mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
			}
		})
	}
}

// TestExtractCorruptIsDegradedNotFatal locks the acceptance guarantee: a
// truncated archive.json yields a valid report (batch id recovered from the
// directory name) with a degraded note, never an error or panic.
func TestExtractCorruptIsDegradedNotFatal(t *testing.T) {
	report, err := retro.Extract(filepath.Join("testdata", "corrupt-json"))
	if err != nil {
		t.Fatalf("Extract on corrupt archive returned error: %v", err)
	}
	if report.BatchID != "corrupt-json" {
		t.Errorf("BatchID = %q, want the directory name %q", report.BatchID, "corrupt-json")
	}
	if !containsSubstr(report.Degraded, "archive.json corrupt") {
		t.Errorf("Degraded = %v, want a note about corrupt archive.json", report.Degraded)
	}
	if len(report.Plans) != 0 {
		t.Errorf("Plans = %d, want 0 when the archive header is unreadable", len(report.Plans))
	}
}

// TestExtractMissingEvidence checks that a plan whose evidence was never
// relocated (and has no execution fallback) is reported as EvidenceMissing with
// a degraded note, while the archive record fields still survive.
func TestExtractMissingEvidence(t *testing.T) {
	report, err := retro.Extract(filepath.Join("testdata", "missing-evidence"))
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(report.Plans) != 2 {
		t.Fatalf("Plans = %d, want 2", len(report.Plans))
	}
	for _, p := range report.Plans {
		if !p.EvidenceMissing {
			t.Errorf("plan %s: EvidenceMissing = false, want true", p.ID)
		}
		if p.Branch == "" {
			t.Errorf("plan %s: Branch dropped; archive record fields must survive a missing evidence tail", p.ID)
		}
	}
}

// TestExtractEmptyBatchDir is the one true error path: a caller mistake, not
// anything on disk.
func TestExtractEmptyBatchDir(t *testing.T) {
	if _, err := retro.Extract("  "); err == nil {
		t.Fatal("Extract(\"  \") returned nil error, want a caller-mistake error")
	}
}

// TestExtractExecutionFallback builds the production-shaped control root in a
// temp dir — archive.json under .springfield/archive/<id> with NO relocated
// plans/ copy, but the live evidence still at .springfield/execution/plans/<key>
// — and asserts Extract falls back to the execution copy and marks the source.
func TestExtractExecutionFallback(t *testing.T) {
	root := t.TempDir()
	batchDir := filepath.Join(root, ".springfield", "archive", "batch-fallback-1")
	writeFile(t, filepath.Join(batchDir, "archive.json"), `{
  "batch_id": "batch-fallback-1",
  "plans": [{"id": "US-050", "title": "Fallback plan", "status": "completed"}]
}`)
	// No batchDir/plans/us-050 — relocation was skipped. Live evidence remains.
	liveDir := filepath.Join(root, ".springfield", "execution", "plans", "us-050", "evidence")
	writeFile(t, filepath.Join(liveDir, "summary.json"), `{"iteration_count":1,"terminal_status":"completed","exit_reason":"done"}`)
	writeFile(t, filepath.Join(liveDir, "iter-1", "meta.json"), `{"agent_id":"claude","exit_code":0}`)

	report, err := retro.Extract(batchDir)
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(report.Plans) != 1 {
		t.Fatalf("Plans = %d, want 1", len(report.Plans))
	}
	p := report.Plans[0]
	if p.EvidenceMissing {
		t.Fatalf("EvidenceMissing = true, want the execution fallback to be found")
	}
	if p.EvidenceSource != "execution" {
		t.Errorf("EvidenceSource = %q, want \"execution\"", p.EvidenceSource)
	}
	if p.IterationCount != 1 || p.TerminalStatus != "completed" {
		t.Errorf("summary not read from fallback: count=%d status=%q", p.IterationCount, p.TerminalStatus)
	}
	if len(p.Iterations) != 1 || p.Iterations[0].AgentID != "claude" {
		t.Errorf("iterations not read from fallback: %+v", p.Iterations)
	}
}

func containsSubstr(notes []string, want string) bool {
	for _, n := range notes {
		if n == want {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
