package planrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/features/conductor"
)

// fixedNow returns a deterministic clock for stamp assertions.
func fixedNow(ts time.Time) func() time.Time { return func() time.Time { return ts } }

// loadedProject builds a minimal on-disk Springfield project and loads it, so
// the internal enterPhase test can exercise the real SaveState/reload path
// without duplicating the external fixture's runner plumbing.
func loadedProject(t *testing.T) (string, *conductor.Project) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}
	prdPath := filepath.Join(root, ".springfield", "plans", "feat", "prd.json")
	if err := os.MkdirAll(filepath.Dir(prdPath), 0o755); err != nil {
		t.Fatalf("mkdir prd: %v", err)
	}
	if err := os.WriteFile(prdPath, []byte(`{"id":"feat"}`), 0o644); err != nil {
		t.Fatalf("write prd: %v", err)
	}
	cfg := map[string]any{
		"plans_dir":     ".springfield/plans",
		"worktree_base": ".worktrees",
		"tool":          "claude",
		"plan_units": []map[string]any{
			{"id": "feat", "path": ".springfield/plans/feat/prd.json", "order": 1},
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
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	return root, project
}

// TestEnterPhasePersistsStamp pins the funnel's happy path: it writes the
// Activity fields onto the plan's PlanState and SaveState makes them durable, so
// a fresh reload (a separate status process) observes the stamp.
func TestEnterPhasePersistsStamp(t *testing.T) {
	root, project := loadedProject(t)
	ts := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	project.State.Plans["feat"] = &conductor.PlanState{Status: conductor.StatusRunning}

	if err := enterPhase(project, "feat", conductor.PhaseImplementing, "US-007", 4, fixedNow(ts)); err != nil {
		t.Fatalf("enterPhase: %v", err)
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	act := reloaded.State.Plans["feat"].Activity
	if act == nil {
		t.Fatal("Activity was not persisted")
	}
	if act.Phase != conductor.PhaseImplementing || act.Detail != "US-007" || act.Round != 4 {
		t.Fatalf("persisted Activity mismatched: %+v", act)
	}
	if !act.UpdatedAt.Equal(ts) {
		t.Fatalf("UpdatedAt = %v, want %v", act.UpdatedAt, ts)
	}
}

// TestEnterPhaseDegradesToSilence guards the "never lie" contract: with no plan
// to stamp onto (nil Project, or a plan with no persisted running state),
// enterPhase is a no-op that neither panics nor fabricates state.
func TestEnterPhaseDegradesToSilence(t *testing.T) {
	if err := enterPhase(nil, "feat", conductor.PhaseImplementing, "US-001", 1, nil); err != nil {
		t.Fatalf("nil project must be a silent no-op, got %v", err)
	}

	_, project := loadedProject(t)
	// No PlanState registered for "feat": nothing truthful to stamp onto.
	if err := enterPhase(project, "feat", conductor.PhaseImplementing, "US-001", 1, nil); err != nil {
		t.Fatalf("missing plan state must be a silent no-op, got %v", err)
	}
	if _, ok := project.State.Plans["feat"]; ok {
		t.Fatal("enterPhase fabricated a PlanState for a plan with none")
	}
}
