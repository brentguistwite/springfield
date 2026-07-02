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

// TestArchivePlanBackwardCompat proves an old 3-field archive record
// deserializes cleanly with the new fields zero-valued (free back-compat).
func TestArchivePlanBackwardCompat(t *testing.T) {
	old := []byte(`{"id":"alpha","title":"Alpha","status":"completed"}`)
	var ap batch.ArchivePlan
	if err := json.Unmarshal(old, &ap); err != nil {
		t.Fatalf("unmarshal old record: %v", err)
	}
	if ap.ID != "alpha" || ap.Title != "Alpha" || ap.Status != "completed" {
		t.Fatalf("legacy fields lost: %+v", ap)
	}
	if ap.Branch != "" || ap.BaseRef != "" || ap.EvidencePath != "" {
		t.Fatalf("new fields must be zero on legacy JSON: %+v", ap)
	}
}

// finalizeFixture builds a project with two batch plan units (alpha, beta) and
// one standalone unit (standalone-x), plus a completed PlanState + evidence dir
// for alpha. Returns the root and the loaded project.
func finalizeFixture(t *testing.T) (string, *conductor.Project, batch.Batch) {
	t.Helper()
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}
	cfg := map[string]any{
		"plans_dir":     ".springfield/plans",
		"worktree_base": ".worktrees",
		"max_retries":   1,
		"tool":          "claude",
		"plan_units": []map[string]any{
			{"id": "alpha", "path": ".springfield/plans/alpha.md", "order": 1},
			{"id": "beta", "path": ".springfield/plans/beta.md", "order": 2},
			{"id": "standalone-x", "path": ".springfield/plans/standalone-x.md", "order": 3},
		},
	}
	cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	plansDir := filepath.Join(root, ".springfield", "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	for _, id := range []string{"alpha", "beta", "standalone-x"} {
		if err := os.WriteFile(filepath.Join(plansDir, id+".md"), []byte("# plan\n"), 0o644); err != nil {
			t.Fatalf("write plan body %s: %v", id, err)
		}
	}

	// Evidence for alpha under execution/plans/alpha/iter-1/cost.json.
	evDir := filepath.Join(root, ".springfield", "execution", "plans", "alpha", "iter-1")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatalf("mkdir evidence: %v", err)
	}
	if err := os.WriteFile(filepath.Join(evDir, "cost.json"), []byte(`{"usd":1.23}`), 0o644); err != nil {
		t.Fatalf("write cost.json: %v", err)
	}

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status:  conductor.StatusCompleted,
		Branch:  "springfield/alpha",
		BaseRef: "develop",
		Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded, Mode: "standalone"},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}
	project.State.Plans["beta"] = &conductor.PlanState{
		Status:  conductor.StatusCompleted,
		Branch:  "springfield/beta",
		BaseRef: "develop",
		Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded, Mode: "standalone"},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// A live run cursor that FinalizeBatch must clear.
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "batch-1"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	b := batch.Batch{
		ID:      "batch-1",
		Title:   "Test Batch",
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"alpha", "beta"}}},
		PlanIDs: []string{"alpha", "beta"},
	}
	return root, project, b
}

func TestFinalizeBatchEnrichesEntryRelocatesEvidenceAndDeregisters(t *testing.T) {
	root, project, b := finalizeFixture(t)
	rollup := &cost.Rollup{TotalUSD: 1.23, PerAdapter: map[string]float64{"claude": 1.23}, Iterations: 1}

	if err := batch.FinalizeBatch(root, b, project, rollup, "per-plan", io.Discard); err != nil {
		t.Fatalf("FinalizeBatch: %v", err)
	}

	// Archive entry written with rollup + per-plan records.
	var entry batch.ArchiveEntry
	archiveData, err := os.ReadFile(batch.StableArchivePath(root, "batch-1"))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if err := json.Unmarshal(archiveData, &entry); err != nil {
		t.Fatalf("decode archive: %v", err)
	}
	if entry.TotalUSD != 1.23 || entry.CostBreakdown["claude"] != 1.23 {
		t.Fatalf("rollup not copied into entry: %+v", entry)
	}
	if len(entry.Plans) != 2 {
		t.Fatalf("want 2 plan records, got %d: %+v", len(entry.Plans), entry.Plans)
	}
	var alpha *batch.ArchivePlan
	for i := range entry.Plans {
		if entry.Plans[i].ID == "alpha" {
			alpha = &entry.Plans[i]
		}
	}
	if alpha == nil {
		t.Fatalf("alpha record missing: %+v", entry.Plans)
	}
	if alpha.Branch != "springfield/alpha" || alpha.BaseRef != "develop" || alpha.Status != "completed" {
		t.Fatalf("alpha record fields wrong: %+v", alpha)
	}
	if alpha.EvidencePath == "" {
		t.Fatalf("alpha EvidencePath must point at relocated evidence")
	}

	// Evidence relocated: source gone, durable copy readable.
	if _, statErr := os.Stat(filepath.Join(root, ".springfield", "execution", "plans", "alpha")); !os.IsNotExist(statErr) {
		t.Fatalf("source evidence must be moved away, stat err = %v", statErr)
	}
	durable := filepath.Join(root, ".springfield", "archive", "batch-1", "plans", "alpha", "iter-1", "cost.json")
	if _, statErr := os.Stat(durable); statErr != nil {
		t.Fatalf("relocated evidence not readable: %v", statErr)
	}

	// Fix 2: batch units deregistered; standalone unit survives.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}
	ids := map[string]bool{}
	for _, u := range reloaded.Config.PlanUnits {
		ids[u.ID] = true
	}
	if ids["alpha"] || ids["beta"] {
		t.Fatalf("batch units must be deregistered, got %+v", reloaded.Config.PlanUnits)
	}
	if !ids["standalone-x"] {
		t.Fatalf("standalone unit must survive, got %+v", reloaded.Config.PlanUnits)
	}

	// State.Plans deletions must be PERSISTED (SaveState), not just in-memory —
	// else completed entries survive on disk and a later batch reusing the IDs
	// reads stale non-pending state.
	if reloaded.State.Plans["alpha"] != nil || reloaded.State.Plans["beta"] != nil {
		t.Fatalf("batch plan state must be cleared on disk after finalize, got %+v", reloaded.State.Plans)
	}

	// run.json cleared.
	if _, _, errRun := batch.ReadRun(root); errRun != nil {
		t.Fatalf("ReadRun after finalize: %v", errRun)
	}
	if _, has, _ := batch.ReadRun(root); has {
		t.Fatalf("run cursor must be cleared after finalize")
	}
}
