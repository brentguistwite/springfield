package batch

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/cost"
)

// A poorer prior entry (the completion fallback writes one with no Plans when
// the project can't be loaded) must not swallow a later, richer same-reason
// entry: maybeWriteArchiveSibling promotes the enriched Plans/cost/mode onto the
// stable file instead of no-oping or merging only cost.
func TestArchiveSiblingPromotesPlansFromRicherSameReasonEntry(t *testing.T) {
	root := t.TempDir()
	b := Batch{ID: "batch-1", Title: "T", PlanIDs: []string{"a"}}

	// 1) Fallback shape: same reason "completed", no Plans, no cost.
	if err := ArchiveBatchNormalizedWithMode(root, b, "completed", &cost.Rollup{}, "per-plan"); err != nil {
		t.Fatalf("fallback archive: %v", err)
	}
	// 2) A later successful finalize re-archives the SAME reason with enriched
	//    Plans and a real rollup.
	records := []ArchivePlan{{ID: "a", Title: "A", Status: "completed", Branch: "springfield/a", BaseRef: "main"}}
	rollup := &cost.Rollup{TotalUSD: 2.0, PerAdapter: map[string]float64{"claude": 2.0}}
	if err := writeEnrichedArchive(root, b, rollup, "per-plan", records); err != nil {
		t.Fatalf("enriched archive: %v", err)
	}

	entry, ok, err := LatestArchive(root)
	if err != nil || !ok {
		t.Fatalf("LatestArchive: ok=%v err=%v", ok, err)
	}
	if len(entry.Plans) != 1 || entry.Plans[0].Branch != "springfield/a" {
		t.Fatalf("enriched Plans must be promoted onto the stable entry, got %+v", entry.Plans)
	}
	if entry.TotalUSD != 2.0 {
		t.Fatalf("cost must also be promoted, got %v", entry.TotalUSD)
	}
	// No spurious sibling — the richer entry overwrote the stable file in place.
	files, _ := os.ReadDir(ArchiveDir(root))
	jsonCount := 0
	for _, f := range files {
		if !f.IsDir() && filepath.Ext(f.Name()) == ".json" {
			jsonCount++
		}
	}
	if jsonCount != 1 {
		t.Fatalf("expected exactly one archive json (no sibling), got %d", jsonCount)
	}
}

// copyTree is the EXDEV (cross-device) fallback for evidence relocation. It must
// preserve a symlink AS a symlink — WalkDir does not follow links, so without
// explicit handling a symlink-to-dir aborts the copy ("is a directory") and a
// symlink-to-file is silently materialized into a regular file.
func TestCopyTreePreservesSymlinks(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")

	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write real file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(src, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("real.txt", filepath.Join(src, "link-to-file")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	if err := os.Symlink("subdir", filepath.Join(src, "link-to-dir")); err != nil {
		t.Fatalf("symlink to dir: %v", err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for _, name := range []string{"link-to-file", "link-to-dir"} {
		info, err := os.Lstat(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s must be preserved as a symlink, got mode %v", name, info.Mode())
		}
	}
	// The regular file still copies through.
	if b, err := os.ReadFile(filepath.Join(dst, "real.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("real.txt copy wrong: %q err=%v", b, err)
	}
}
