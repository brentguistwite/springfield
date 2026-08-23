package retro_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/features/retro"
)

// writeArchivedReport materializes <root>/.springfield/archive/<batchID>/retro.json
// with the given report, mirroring the on-disk layout Aggregate globs. Fixtures
// are built at runtime rather than checked into testdata so a multi-root corpus
// stays self-describing in the test and never persists a .springfield tree.
func writeArchivedReport(t *testing.T, root, batchID string, r retro.Report) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "archive", batchID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir archive %s: %v", dir, err)
	}
	if err := retro.WriteReport(dir, &r); err != nil {
		t.Fatalf("write retro.json for %s: %v", batchID, err)
	}
}

func atTime(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}

func findPattern(patterns []retro.Pattern, key string) (retro.Pattern, bool) {
	for _, p := range patterns {
		if p.Key == key {
			return p, true
		}
	}
	return retro.Pattern{}, false
}

// TestAggregateRollsUpAcrossRoots is the core AC: overlapping pattern keys
// spread across two project roots and multiple batches fold into single rows
// carrying the distinct batch IDs, distinct project roots, and freshest
// last-seen. One fixture runs in consolidate batch-mode to pin that pattern
// keys are mode-independent — a consolidate report's findings aggregate the
// same as a per-plan report's.
func TestAggregateRollsUpAcrossRoots(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()

	writeArchivedReport(t, rootA, "batch-a1", retro.Report{
		BatchID:    "batch-a1",
		BatchMode:  "per-plan",
		ArchivedAt: atTime("2026-08-01T00:00:00Z"),
		Findings: []retro.Finding{
			{PatternKey: "iteration-cap", Severity: "high", PlanIDs: []string{"US-001"}},
			{PatternKey: "fallback-storm", Severity: "medium", PlanIDs: []string{"US-002"}},
		},
	})
	writeArchivedReport(t, rootA, "batch-a2", retro.Report{
		BatchID:    "batch-a2",
		BatchMode:  "per-plan",
		ArchivedAt: atTime("2026-08-05T00:00:00Z"),
		Findings: []retro.Finding{
			{PatternKey: "iteration-cap", Severity: "high", PlanIDs: []string{"US-010"}},
		},
	})
	// A consolidate-mode report on a second root, tripping the same key: mode
	// must not change the key, so this occurrence merges with rootA's.
	writeArchivedReport(t, rootB, "batch-b1", retro.Report{
		BatchID:    "batch-b1",
		BatchMode:  "consolidate",
		ArchivedAt: atTime("2026-08-10T00:00:00Z"),
		Findings: []retro.Finding{
			{PatternKey: "iteration-cap", Severity: "high", PlanIDs: []string{"US-020"}},
		},
	})

	patterns, err := retro.Aggregate([]string{rootA, rootB}, time.Time{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	cap, ok := findPattern(patterns, "iteration-cap")
	if !ok {
		t.Fatal("iteration-cap pattern missing")
	}
	if cap.Occurrences != 3 {
		t.Errorf("iteration-cap Occurrences = %d, want 3", cap.Occurrences)
	}
	wantBatches := []string{"batch-a1", "batch-a2", "batch-b1"}
	if !equalStrings(cap.Batches, wantBatches) {
		t.Errorf("iteration-cap Batches = %v, want %v", cap.Batches, wantBatches)
	}
	wantProjects := []string{rootA, rootB}
	if !equalStrings(cap.Projects, sortStrings(wantProjects)) {
		t.Errorf("iteration-cap Projects = %v, want %v", cap.Projects, sortStrings(wantProjects))
	}
	if !cap.LastSeen.Equal(atTime("2026-08-10T00:00:00Z")) {
		t.Errorf("iteration-cap LastSeen = %v, want 2026-08-10", cap.LastSeen)
	}

	storm, ok := findPattern(patterns, "fallback-storm")
	if !ok {
		t.Fatal("fallback-storm pattern missing")
	}
	if storm.Occurrences != 1 {
		t.Errorf("fallback-storm Occurrences = %d, want 1", storm.Occurrences)
	}
	if !equalStrings(storm.Projects, []string{rootA}) {
		t.Errorf("fallback-storm Projects = %v, want [%s]", storm.Projects, rootA)
	}
}

// TestAggregateSortsByOccurrencesThenKey pins the deterministic ordering AC:
// occurrences descending, ties broken by key ascending.
func TestAggregateSortsByOccurrencesThenKey(t *testing.T) {
	root := t.TempDir()
	// "zeta" and "alpha" each fire once (a tie -> alpha before zeta);
	// "beta" fires twice (leads regardless of key).
	writeArchivedReport(t, root, "batch-1", retro.Report{
		BatchID:    "batch-1",
		ArchivedAt: atTime("2026-08-01T00:00:00Z"),
		Findings: []retro.Finding{
			{PatternKey: "zeta"},
			{PatternKey: "beta"},
		},
	})
	writeArchivedReport(t, root, "batch-2", retro.Report{
		BatchID:    "batch-2",
		ArchivedAt: atTime("2026-08-02T00:00:00Z"),
		Findings: []retro.Finding{
			{PatternKey: "alpha"},
			{PatternKey: "beta"},
		},
	})

	patterns, err := retro.Aggregate([]string{root}, time.Time{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	got := []string{}
	for _, p := range patterns {
		got = append(got, p.Key)
	}
	want := []string{"beta", "alpha", "zeta"}
	if !equalStrings(got, want) {
		t.Errorf("sort order = %v, want %v", got, want)
	}
}

// TestAggregateSkipsEmptyAndMissingRoots covers the tolerance AC for absent
// inputs: a root that does not exist and a root with no .springfield tree both
// contribute nothing without erroring.
func TestAggregateSkipsEmptyAndMissingRoots(t *testing.T) {
	present := t.TempDir()
	writeArchivedReport(t, present, "batch-1", retro.Report{
		BatchID:    "batch-1",
		ArchivedAt: atTime("2026-08-01T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
	emptyRoot := t.TempDir() // exists but has no .springfield

	patterns, err := retro.Aggregate([]string{nonexistent, emptyRoot, present}, time.Time{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(patterns) != 1 || patterns[0].Key != "iteration-cap" {
		t.Fatalf("patterns = %+v, want single iteration-cap from the present root", patterns)
	}
	if !equalStrings(patterns[0].Projects, []string{present}) {
		t.Errorf("Projects = %v, want only the present root", patterns[0].Projects)
	}
}

// TestAggregateSkipsCorruptFile covers the corrupt-file AC: one unparseable
// retro.json is skipped on its own while its healthy siblings still roll up.
func TestAggregateSkipsCorruptFile(t *testing.T) {
	root := t.TempDir()
	writeArchivedReport(t, root, "batch-good", retro.Report{
		BatchID:    "batch-good",
		ArchivedAt: atTime("2026-08-01T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})
	// A sibling batch dir whose retro.json is not valid JSON.
	badDir := filepath.Join(root, ".springfield", "archive", "batch-bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir bad dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "retro.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt retro.json: %v", err)
	}

	patterns, err := retro.Aggregate([]string{root}, time.Time{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(patterns) != 1 || patterns[0].Occurrences != 1 {
		t.Fatalf("patterns = %+v, want single iteration-cap surviving the corrupt sibling", patterns)
	}
}

// TestAggregateSinceWindowFilters covers the since-window AC: a report archived
// at or before since is excluded; one archived after it contributes.
func TestAggregateSinceWindowFilters(t *testing.T) {
	root := t.TempDir()
	writeArchivedReport(t, root, "batch-old", retro.Report{
		BatchID:    "batch-old",
		ArchivedAt: atTime("2026-07-01T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})
	writeArchivedReport(t, root, "batch-new", retro.Report{
		BatchID:    "batch-new",
		ArchivedAt: atTime("2026-08-15T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})

	since := atTime("2026-08-01T00:00:00Z")
	patterns, err := retro.Aggregate([]string{root}, since)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(patterns) != 1 {
		t.Fatalf("patterns = %+v, want one row", patterns)
	}
	p := patterns[0]
	if p.Occurrences != 1 {
		t.Errorf("Occurrences = %d, want 1 (old report filtered out)", p.Occurrences)
	}
	if !equalStrings(p.Batches, []string{"batch-new"}) {
		t.Errorf("Batches = %v, want [batch-new]", p.Batches)
	}
}

// TestAggregateSinceIsExclusiveOnBoundary pins that since is a strict lower
// bound: a report archived exactly at since is filtered out.
func TestAggregateSinceIsExclusiveOnBoundary(t *testing.T) {
	root := t.TempDir()
	boundary := atTime("2026-08-01T00:00:00Z")
	writeArchivedReport(t, root, "batch-boundary", retro.Report{
		BatchID:    "batch-boundary",
		ArchivedAt: boundary,
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})
	patterns, err := retro.Aggregate([]string{root}, boundary)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(patterns) != 0 {
		t.Errorf("patterns = %+v, want empty (boundary report is not after since)", patterns)
	}
}

// TestAggregateNilRootsErrors covers the one true error path: nil roots is the
// unusable input that means nothing was asked.
func TestAggregateNilRootsErrors(t *testing.T) {
	if _, err := retro.Aggregate(nil, time.Time{}); err == nil {
		t.Fatal("Aggregate(nil) returned nil error, want an unusable-input error")
	}
}

// TestAggregateEmptyRootsIsNotAnError distinguishes an empty (but non-nil)
// slice — a legitimate "no roots to scan" — from the nil caller mistake.
func TestAggregateEmptyRootsIsNotAnError(t *testing.T) {
	patterns, err := retro.Aggregate([]string{}, time.Time{})
	if err != nil {
		t.Fatalf("Aggregate([]): %v", err)
	}
	if len(patterns) != 0 {
		t.Errorf("patterns = %+v, want empty", patterns)
	}
}

// TestAggregatePerformsNoWrites is the purity AC: Aggregate must not create,
// modify, or rename anything under the roots it scans. We snapshot the full
// tree before and after and assert byte-for-byte equality.
func TestAggregatePerformsNoWrites(t *testing.T) {
	root := t.TempDir()
	writeArchivedReport(t, root, "batch-1", retro.Report{
		BatchID:    "batch-1",
		ArchivedAt: atTime("2026-08-01T00:00:00Z"),
		Findings:   []retro.Finding{{PatternKey: "iteration-cap"}},
	})

	before := snapshotTree(t, root)
	if _, err := retro.Aggregate([]string{root}, time.Time{}); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	after := snapshotTree(t, root)

	if len(before) != len(after) {
		t.Fatalf("file count changed: before %d, after %d", len(before), len(after))
	}
	for path, sum := range before {
		if after[path] != sum {
			t.Errorf("file %s changed or was added/removed by Aggregate", path)
		}
	}
}

// snapshotTree records every regular file's relative path and contents under
// dir, so a later snapshot can prove nothing was written.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
