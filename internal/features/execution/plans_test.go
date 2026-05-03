package execution_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/execution"
)

func TestAddPlanPersistsToConfig(t *testing.T) {
	root := newProject(t)
	writePlanFile(t, root, "feature.md")

	if _, err := execution.AddPlan(root, execution.PlanInput{
		ID:    "feature-a",
		Title: "Feature A",
		Path:  "feature.md",
	}); err != nil {
		t.Fatalf("AddPlan: %v", err)
	}

	cfg := readConfig(t, root)
	units, ok := cfg["plan_units"].([]any)
	if !ok || len(units) != 1 {
		t.Fatalf("plan_units missing or empty: %v", cfg["plan_units"])
	}
	first := units[0].(map[string]any)
	if first["id"] != "feature-a" {
		t.Fatalf("id = %v", first["id"])
	}
	if first["path"] != "springfield/plans/feature.md" {
		t.Fatalf("path = %v", first["path"])
	}
}

func TestReorderPlansPersists(t *testing.T) {
	root := newProject(t)
	writePlanFile(t, root, "a.md")
	writePlanFile(t, root, "b.md")

	if _, err := execution.AddPlan(root, execution.PlanInput{ID: "a", Path: "a.md"}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := execution.AddPlan(root, execution.PlanInput{ID: "b", Path: "b.md"}); err != nil {
		t.Fatalf("add b: %v", err)
	}
	if err := execution.ReorderPlans(root, []string{"b", "a"}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	plans, err := execution.ListPlans(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(plans) != 2 || plans[0].ID != "b" || plans[1].ID != "a" {
		t.Fatalf("got %v, want [b a]", plans)
	}
}

func TestRegistryStatusSurfacesNextStep(t *testing.T) {
	root := newProject(t)
	writePlanFile(t, root, "feature.md")
	if _, err := execution.AddPlan(root, execution.PlanInput{ID: "feature-a", Path: "feature.md"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	rs, err := execution.LoadRegistryStatus(root)
	if err != nil {
		t.Fatalf("LoadRegistryStatus: %v", err)
	}
	if rs.Total != 1 || rs.Completed != 0 {
		t.Fatalf("counts: total=%d completed=%d", rs.Total, rs.Completed)
	}
	if strings.Contains(rs.NextStep, "springfield start") {
		t.Fatalf("registry status must not point at springfield start in slice 1: %q", rs.NextStep)
	}
	if !strings.Contains(rs.NextStep, "does not execute") {
		t.Fatalf("next step should explain execution lands later: %q", rs.NextStep)
	}
}

func TestLegacyConfigStillLoads(t *testing.T) {
	root := newProject(t)
	// Write a config without plan_units — the pre-slice schema.
	cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{
  "plans_dir": "springfield/plans",
  "worktree_base": ".worktrees",
  "max_retries": 1,
  "single_workstream_iterations": 10,
  "single_workstream_timeout": 600,
  "tool": "claude",
  "batches": [["legacy-a"]],
  "sequential": ["legacy-b"]
}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loaded, err := execution.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Tool != "claude" {
		t.Fatalf("tool = %q", loaded.Tool)
	}
	if len(loaded.Sequential) != 1 || loaded.Sequential[0] != "legacy-b" {
		t.Fatalf("sequential lost: %v", loaded.Sequential)
	}
	if len(loaded.Batches) != 1 || loaded.Batches[0][0] != "legacy-a" {
		t.Fatalf("batches lost: %v", loaded.Batches)
	}
}

// --- helpers ---

func newProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"), []byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	cfg := map[string]any{
		"plans_dir":                    "springfield/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"single_workstream_timeout":    600,
		"tool":                         "claude",
		"batches":                      [][]string{},
		"sequential":                   []string{},
	}
	cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

func writePlanFile(t *testing.T, root, file string) {
	t.Helper()
	dir := filepath.Join(root, "springfield", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("# plan"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

func readConfig(t *testing.T, root string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return out
}
