package retro_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/retro"
)

// filedItems returns the names of the auto-filed *.md tickets under itemsDir,
// ignoring the id-lock and any non-item files, so a test can assert exactly
// which pattern keys were filed.
func filedItems(t *testing.T, itemsDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(itemsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read items dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "-auto.md") {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// TestFileRecurringFilesOnlyAboveThreshold is the core wiring AC: a pattern that
// clears both bars (>= MinOccurrences findings across >= MinBatches distinct
// batches) is filed as a vault ticket, while a pattern that recurs within a
// single batch — noise, not a cross-run trend — is not.
func TestFileRecurringFilesOnlyAboveThreshold(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	itemsDir := filepath.Join(t.TempDir(), "items")

	// "iteration-cap" spans three batches across two roots -> above threshold.
	writeArchivedReport(t, rootA, "batch-a1", retro.Report{
		BatchID:    "batch-a1",
		ArchivedAt: atTime("2026-08-01T00:00:00Z"),
		Findings: []retro.Finding{
			{PatternKey: "iteration-cap"},
			// A single-batch repeat of a distinct key: noise, must not file.
			{PatternKey: "fallback-storm"},
			{PatternKey: "fallback-storm"},
			{PatternKey: "fallback-storm"},
		},
	})
	writeArchivedReport(t, rootA, "batch-a2", retro.Report{
		BatchID:    "batch-a2",
		ArchivedAt: atTime("2026-08-05T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})
	writeArchivedReport(t, rootB, "batch-b1", retro.Report{
		BatchID:    "batch-b1",
		ArchivedAt: atTime("2026-08-10T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})

	results, err := retro.FileRecurring([]string{rootA, rootB}, itemsDir, time.Time{})
	if err != nil {
		t.Fatalf("FileRecurring: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want exactly one filed ticket (iteration-cap)", results)
	}
	if !results[0].Created {
		t.Errorf("expected the iteration-cap ticket to be freshly created")
	}

	filed := filedItems(t, itemsDir)
	if len(filed) != 1 || !strings.Contains(filed[0], "iteration-cap") {
		t.Fatalf("filed items = %v, want a single iteration-cap ticket", filed)
	}
	// fallback-storm cleared MinOccurrences (3) but only within one batch, so it
	// fails MinBatches and must not have been filed.
	for _, name := range filed {
		if strings.Contains(name, "fallback-storm") {
			t.Errorf("fallback-storm was filed despite spanning a single batch: %s", name)
		}
	}
}

// TestFileRecurringIsIdempotent proves a second run over the same corpus updates
// the existing ticket rather than minting a duplicate: the dedup contract of the
// filer holds end to end through the aggregator.
func TestFileRecurringIsIdempotent(t *testing.T) {
	root := t.TempDir()
	itemsDir := filepath.Join(t.TempDir(), "items")
	writeArchivedReport(t, root, "batch-1", retro.Report{
		BatchID:    "batch-1",
		ArchivedAt: atTime("2026-08-01T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}, {PatternKey: "iteration-cap"}},
	})
	writeArchivedReport(t, root, "batch-2", retro.Report{
		BatchID:    "batch-2",
		ArchivedAt: atTime("2026-08-02T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})

	first, err := retro.FileRecurring([]string{root}, itemsDir, time.Time{})
	if err != nil {
		t.Fatalf("first FileRecurring: %v", err)
	}
	if len(first) != 1 || !first[0].Created {
		t.Fatalf("first run = %+v, want one created ticket", first)
	}
	second, err := retro.FileRecurring([]string{root}, itemsDir, time.Time{})
	if err != nil {
		t.Fatalf("second FileRecurring: %v", err)
	}
	if len(second) != 1 || second[0].Created {
		t.Fatalf("second run = %+v, want one updated (not created) ticket", second)
	}
	if len(filedItems(t, itemsDir)) != 1 {
		t.Errorf("expected exactly one ticket on disk after two runs, got %v", filedItems(t, itemsDir))
	}
}

// TestFileRecurringToleratesBadCorpus mirrors Aggregate's tolerance: a corrupt
// retro.json and a nonexistent root are skipped without sinking the qualifying
// pattern assembled from the healthy siblings.
func TestFileRecurringToleratesBadCorpus(t *testing.T) {
	root := t.TempDir()
	itemsDir := filepath.Join(t.TempDir(), "items")
	writeArchivedReport(t, root, "batch-1", retro.Report{
		BatchID:    "batch-1",
		ArchivedAt: atTime("2026-08-01T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}, {PatternKey: "iteration-cap"}},
	})
	writeArchivedReport(t, root, "batch-2", retro.Report{
		BatchID:    "batch-2",
		ArchivedAt: atTime("2026-08-02T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})
	badDir := filepath.Join(root, ".springfield", "archive", "batch-bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir bad dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "retro.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt retro.json: %v", err)
	}
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")

	results, err := retro.FileRecurring([]string{nonexistent, root}, itemsDir, time.Time{})
	if err != nil {
		t.Fatalf("FileRecurring: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one iteration-cap ticket surviving the corrupt sibling", results)
	}
}

// TestFileRecurringSinceWindowFilters proves the recency window flows through to
// filing: an old report excluded by since cannot help a pattern clear the batch
// spread, so a pattern that would qualify over all history stays unfiled.
func TestFileRecurringSinceWindowFilters(t *testing.T) {
	root := t.TempDir()
	itemsDir := filepath.Join(t.TempDir(), "items")
	writeArchivedReport(t, root, "batch-old", retro.Report{
		BatchID:    "batch-old",
		ArchivedAt: atTime("2026-07-01T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}, {PatternKey: "iteration-cap"}},
	})
	writeArchivedReport(t, root, "batch-new", retro.Report{
		BatchID:    "batch-new",
		ArchivedAt: atTime("2026-08-15T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})

	// Windowed to after the old batch: only one batch (batch-new) survives, so
	// the pattern fails MinBatches and nothing is filed.
	results, err := retro.FileRecurring([]string{root}, itemsDir, atTime("2026-08-01T00:00:00Z"))
	if err != nil {
		t.Fatalf("FileRecurring: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want none (windowed corpus fails batch spread)", results)
	}
	if len(filedItems(t, itemsDir)) != 0 {
		t.Errorf("expected no tickets filed, got %v", filedItems(t, itemsDir))
	}
}

// TestFileRecurringNilRootsErrors covers the caller-mistake input: nil roots is
// "nothing was asked", surfaced before any file is touched.
func TestFileRecurringNilRootsErrors(t *testing.T) {
	itemsDir := filepath.Join(t.TempDir(), "items")
	if _, err := retro.FileRecurring(nil, itemsDir, time.Time{}); err == nil {
		t.Fatal("FileRecurring(nil roots) returned nil error, want an unusable-input error")
	}
}

// TestFileRecurringDisabledFilerErrors covers the other unusable input: an empty
// itemsDir means the filer is disabled, which is a caller mistake here (the call
// site only invokes filing when items_dir is configured).
func TestFileRecurringDisabledFilerErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := retro.FileRecurring([]string{root}, "", time.Time{}); err == nil {
		t.Fatal("FileRecurring with empty itemsDir returned nil error, want a disabled-filer error")
	}
}
