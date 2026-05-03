package conductor_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/storage"
)

func TestNormalizePlanPathAcceptsBareFilename(t *testing.T) {
	got, err := conductor.NormalizePlanPath("springfield/plans", "feature.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "springfield/plans/feature.md" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizePlanPathAcceptsRelativeUnderPlansDir(t *testing.T) {
	got, err := conductor.NormalizePlanPath("springfield/plans", "springfield/plans/sub/feature.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "springfield/plans/sub/feature.md" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizePlanPathRejectsAbsolute(t *testing.T) {
	_, err := conductor.NormalizePlanPath("springfield/plans", "/etc/plan.md")
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute rejection, got %v", err)
	}
}

func TestNormalizePlanPathRejectsEscape(t *testing.T) {
	_, err := conductor.NormalizePlanPath("springfield/plans", "../etc/plan.md")
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected escape rejection, got %v", err)
	}
}

func TestNormalizePlanPathRejectsOutsidePlansDir(t *testing.T) {
	_, err := conductor.NormalizePlanPath("springfield/plans", "other/loc/plan.md")
	if err == nil {
		t.Fatalf("expected rejection for path outside plans_dir")
	}
}

func TestValidateRefAcceptsCommonBranchNames(t *testing.T) {
	for _, ok := range []string{"main", "feature/abc", "release-1.2"} {
		if err := conductor.ValidateRef(ok); err != nil {
			t.Fatalf("ref %q rejected: %v", ok, err)
		}
	}
	if err := conductor.ValidateRef(""); err != nil {
		t.Fatalf("empty ref should be accepted: %v", err)
	}
}

func TestValidateRefRejectsBadInput(t *testing.T) {
	for _, bad := range []string{" main", "feat..ure", "-leading-dash", "white space"} {
		if err := conductor.ValidateRef(bad); err == nil {
			t.Fatalf("ref %q accepted, want rejection", bad)
		}
	}
}

func TestValidatePlanUnitIDRejectsBadInput(t *testing.T) {
	for _, bad := range []string{"", "Cap", "with space", "_leading"} {
		if err := conductor.ValidatePlanUnitID(bad); err == nil {
			t.Fatalf("id %q accepted, want rejection", bad)
		}
	}
}

func TestAddPlanUnitWritesConfigAndAssignsOrder(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")

	project := loadProjectWithDefaults(t, root, plansDir)

	first, err := project.AddPlanUnit(conductor.PlanUnitInput{
		ID:    "feature-a",
		Title: "Feature A",
		Path:  "feature.md",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if first.Order != 1 {
		t.Fatalf("first order = %d, want 1", first.Order)
	}
	if first.Path != "springfield/plans/feature.md" {
		t.Fatalf("path = %q", first.Path)
	}

	writeFile(t, filepath.Join(root, "springfield/plans/second.md"), "# second")
	second, err := project.AddPlanUnit(conductor.PlanUnitInput{
		ID:    "feature-b",
		Title: "Feature B",
		Path:  "second.md",
	})
	if err != nil {
		t.Fatalf("add second: %v", err)
	}
	if second.Order != 2 {
		t.Fatalf("second order = %d, want 2", second.Order)
	}
}

func TestAddPlanUnitRejectsDuplicateID(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "feature-a", Path: "feature.md"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "feature-a", Path: "feature.md"}); err == nil {
		t.Fatalf("expected duplicate id rejection")
	}
}

func TestAddPlanUnitRejectsMissingFile(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "ghost", Path: "ghost.md"}); err == nil {
		t.Fatalf("expected missing-file rejection")
	}
}

func TestAddPlanUnitRejectsBadRef(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "feature-a", Path: "feature.md", Ref: "bad ref"}); err == nil {
		t.Fatalf("expected bad ref rejection")
	}
}

func TestAddPlanUnitRejectsAbsolutePath(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "feature-a", Path: "/abs/feature.md"}); err == nil {
		t.Fatalf("expected absolute-path rejection")
	}
}

func TestAddPlanUnitRejectsExplicitOrderClash(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "a", Path: "feature.md", Order: 1}); err != nil {
		t.Fatalf("add: %v", err)
	}
	writeFile(t, filepath.Join(root, "springfield/plans/second.md"), "# second")
	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "b", Path: "second.md", Order: 1}); err == nil {
		t.Fatalf("expected duplicate order rejection")
	}
}

func TestReorderPlanUnits(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	writeFile(t, filepath.Join(root, "springfield/plans/second.md"), "# second")
	project := loadProjectWithDefaults(t, root, plansDir)

	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "a", Path: "feature.md"}); err != nil {
		t.Fatalf("add a: %v", err)
	}
	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "b", Path: "second.md"}); err != nil {
		t.Fatalf("add b: %v", err)
	}

	if err := project.ReorderPlanUnits([]string{"b", "a"}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	got := conductor.OrderedPlanUnitIDs(project.Config.PlanUnits)
	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("got %v, want [b a]", got)
	}
}

func TestReorderPlanUnitsRejectsMissingID(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "a", Path: "feature.md"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := project.ReorderPlanUnits([]string{"a", "b"}); err == nil {
		t.Fatalf("expected length mismatch rejection")
	}
}

func TestRemovePlanUnitClearsState(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	if _, err := project.AddPlanUnit(conductor.PlanUnitInput{ID: "a", Path: "feature.md"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	project.MarkRunning("a")

	if err := project.RemovePlanUnit("a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(project.Config.PlanUnits) != 0 {
		t.Fatalf("plan units not cleared")
	}
	if _, ok := project.State.Plans["a"]; ok {
		t.Fatalf("state for a not cleared")
	}
}

func TestLoadProjectRejectsDuplicatePlanUnitOrder(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	writeFile(t, filepath.Join(root, "springfield/plans/second.md"), "# second")
	writeOnDiskConfigJSON(t, root, plansDir, []map[string]any{
		{"id": "a", "path": "springfield/plans/feature.md", "order": 1},
		{"id": "b", "path": "springfield/plans/second.md", "order": 1},
	})

	_, err := conductor.LoadProject(root)
	if err == nil || !strings.Contains(err.Error(), "duplicate plan unit order") {
		t.Fatalf("expected duplicate-order rejection, got %v", err)
	}
}

func TestLoadProjectRejectsMissingPlanFile(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	writeOnDiskConfigJSON(t, root, plansDir, []map[string]any{
		{"id": "a", "path": "springfield/plans/ghost.md", "order": 1},
	})

	_, err := conductor.LoadProject(root)
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("expected missing-file rejection, got %v", err)
	}
}

func TestLoadProjectRejectsBadRef(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	writeOnDiskConfigJSON(t, root, plansDir, []map[string]any{
		{"id": "a", "path": "springfield/plans/feature.md", "order": 1, "ref": "bad ref"},
	})

	_, err := conductor.LoadProject(root)
	if err == nil || !strings.Contains(err.Error(), "ref") {
		t.Fatalf("expected bad-ref rejection, got %v", err)
	}
}

func TestLoadProjectRejectsAbsolutePath(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	writeOnDiskConfigJSON(t, root, plansDir, []map[string]any{
		{"id": "a", "path": "/etc/feature.md", "order": 1},
	})

	_, err := conductor.LoadProject(root)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected abs-path rejection, got %v", err)
	}
}

func TestSaveConfigRejectsInvalidPlanUnits(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	project.Config.PlanUnits = []conductor.PlanUnit{
		{ID: "BAD ID", Path: "springfield/plans/feature.md", Order: 1},
	}
	if err := project.SaveConfig(); err == nil || !strings.Contains(err.Error(), "invalid plan unit id") {
		t.Fatalf("expected invalid-id rejection, got %v", err)
	}
}

func TestValidateConfigPlanUnitsDuplicateOrder(t *testing.T) {
	cfg := &conductor.Config{
		PlansDir: "springfield/plans",
		PlanUnits: []conductor.PlanUnit{
			{ID: "a", Path: "springfield/plans/a.md", Order: 1},
			{ID: "b", Path: "springfield/plans/b.md", Order: 1},
		},
	}
	if err := conductor.ValidateConfigPlanUnits(cfg, ""); err == nil || !strings.Contains(err.Error(), "duplicate plan unit order") {
		t.Fatalf("expected duplicate order error, got %v", err)
	}
}

// --- helpers ---

func newProjectRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	body := `[project]
agent_priority = ["claude"]
`
	writeFile(t, filepath.Join(root, "springfield.toml"), body)
	return root
}

func writeProjectAndPlan(t *testing.T, root, planFile string) string {
	t.Helper()
	plansDir := "springfield/plans"
	if err := os.MkdirAll(filepath.Join(root, plansDir), 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	writeFile(t, filepath.Join(root, plansDir, planFile), "# plan body")
	return plansDir
}

func loadProjectWithDefaults(t *testing.T, root, plansDir string) *conductor.Project {
	t.Helper()
	rt, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("FromRoot: %v", err)
	}
	cfg := &conductor.Config{
		PlansDir:                   plansDir,
		WorktreeBase:               ".worktrees",
		MaxRetries:                 1,
		SingleWorkstreamIterations: 10,
		SingleWorkstreamTimeout:    600,
		Tool:                       "claude",
	}
	if err := rt.WriteJSON("execution/config.json", cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	return project
}

func writeOnDiskConfigJSON(t *testing.T, root, plansDir string, planUnits []map[string]any) {
	t.Helper()
	cfg := map[string]any{
		"plans_dir":                    plansDir,
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"single_workstream_timeout":    600,
		"tool":                         "claude",
		"batches":                      [][]string{},
		"sequential":                   []string{},
		"plan_units":                   planUnits,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	full := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
