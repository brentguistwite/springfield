package batch_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
)

// TestFinalizeBatchConvergesAfterPartialRelocateCrash simulates a crash where
// the archive entry is already written and ONE plan's evidence is already
// relocated while the other is not, then re-runs FinalizeBatch. The re-run must
// converge: no spurious sibling archive, both plans' evidence at the durable
// path, units deregistered exactly once, run cursor cleared.
func TestFinalizeBatchConvergesAfterPartialRelocateCrash(t *testing.T) {
	root, project, b := finalizeFixture(t)
	// finalizeFixture registers alpha+beta (batch) and standalone-x, with
	// evidence only for alpha. Give beta evidence too so we have two movers.
	betaEv := filepath.Join(root, ".springfield", "execution", "plans", "beta", "iter-1")
	if err := os.MkdirAll(betaEv, 0o755); err != nil {
		t.Fatalf("mkdir beta evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(betaEv, "cost.json"), []byte(`{"usd":0.5}`), 0o644); err != nil {
		t.Fatalf("write beta cost: %v", err)
	}

	// Simulate the first (crashed) finalize: archive entry written, alpha already
	// relocated, beta still in execution/.
	archiveDir := filepath.Join(root, ".springfield", "archive")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	// A realistic crash leaves the ENRICHED entry: writeEnrichedArchive writes
	// the Plans-bearing record (step 2) before any relocate (step 3) runs, so a
	// crash mid-relocate finds Plans already present. Re-convergence must NOT
	// clobber them (maybeWriteArchiveSibling no-ops on the matching reason).
	preEntry := batch.ArchiveEntry{
		BatchID: "batch-1", Title: "Test Batch", Reason: "completed", BatchMode: "per-plan",
		Plans: []batch.ArchivePlan{
			{ID: "alpha", Title: "Alpha", Status: "completed", Branch: "springfield/alpha", BaseRef: "develop"},
			{ID: "beta", Title: "Beta", Status: "completed", Branch: "springfield/beta", BaseRef: "develop"},
		},
	}
	data, _ := json.MarshalIndent(preEntry, "", "  ")
	if err := os.WriteFile(filepath.Join(archiveDir, "batch-1.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("pre-write entry: %v", err)
	}
	alphaSrc := filepath.Join(root, ".springfield", "execution", "plans", "alpha")
	alphaDst := filepath.Join(archiveDir, "batch-1", "plans", "alpha")
	if err := os.MkdirAll(filepath.Dir(alphaDst), 0o755); err != nil {
		t.Fatalf("mkdir alpha dst: %v", err)
	}
	if err := os.Rename(alphaSrc, alphaDst); err != nil {
		t.Fatalf("pre-relocate alpha: %v", err)
	}

	// Re-run finalize (the resume).
	if err := batch.FinalizeBatch(root, b, project, &cost.Rollup{}, "per-plan", io.Discard); err != nil {
		t.Fatalf("FinalizeBatch re-run: %v", err)
	}

	// No spurious sibling archive (only batch-1.json should exist).
	entries, _ := os.ReadDir(archiveDir)
	for _, e := range entries {
		if !e.IsDir() && e.Name() != "batch-1.json" {
			t.Fatalf("unexpected extra archive file after re-run: %s", e.Name())
		}
	}
	// The enriched entry survives re-convergence intact — Plans not clobbered.
	var finalEntry batch.ArchiveEntry
	entryData, err := os.ReadFile(filepath.Join(archiveDir, "batch-1.json"))
	if err != nil {
		t.Fatalf("read entry after re-run: %v", err)
	}
	if err := json.Unmarshal(entryData, &finalEntry); err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	if len(finalEntry.Plans) != 2 {
		t.Fatalf("entry Plans must be preserved (want 2), got %d: %+v", len(finalEntry.Plans), finalEntry.Plans)
	}
	for _, p := range finalEntry.Plans {
		if p.Branch == "" {
			t.Fatalf("plan %s lost its Branch on re-convergence: %+v", p.ID, p)
		}
	}
	// Both plans' evidence at the durable path; sources gone.
	for _, id := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(archiveDir, "batch-1", "plans", id, "iter-1", "cost.json")); err != nil {
			t.Fatalf("plan %s evidence must be at durable path: %v", id, err)
		}
		if _, err := os.Stat(filepath.Join(root, ".springfield", "execution", "plans", id)); !os.IsNotExist(err) {
			t.Fatalf("plan %s source evidence must be gone, stat err=%v", id, err)
		}
	}
	// Units deregistered exactly once; standalone survives.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	ids := map[string]bool{}
	for _, u := range reloaded.Config.PlanUnits {
		ids[u.ID] = true
	}
	if ids["alpha"] || ids["beta"] {
		t.Fatalf("batch units must be deregistered, got %+v", reloaded.Config.PlanUnits)
	}
	if !ids["standalone-x"] {
		t.Fatal("standalone unit must survive")
	}
	// Run cursor cleared.
	if _, ok, _ := batch.ReadRun(root); ok {
		t.Fatal("run cursor must be cleared")
	}
}
