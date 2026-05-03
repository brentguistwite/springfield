package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlansAddListReorderRemoveRoundtrip(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusPlan(t, root, "second.md")
	writeStatusConfig(t, root, []map[string]any{})

	if out, err := runPlansArgs(t, "add", "--dir", root, "--id", "feature-a", "--title", "Feature A", "--path", "feature.md"); err != nil {
		t.Fatalf("add a: %v\n%s", err, out)
	}
	if out, err := runPlansArgs(t, "add", "--dir", root, "--id", "feature-b", "--title", "Feature B", "--path", "second.md"); err != nil {
		t.Fatalf("add b: %v\n%s", err, out)
	}

	listOut, err := runPlansArgs(t, "list", "--dir", root)
	if err != nil {
		t.Fatalf("list: %v\n%s", err, listOut)
	}
	if !strings.Contains(listOut, "1. feature-a") || !strings.Contains(listOut, "2. feature-b") {
		t.Fatalf("list ordering wrong:\n%s", listOut)
	}

	if out, err := runPlansArgs(t, "reorder", "--dir", root, "feature-b", "feature-a"); err != nil {
		t.Fatalf("reorder: %v\n%s", err, out)
	}
	listOut, _ = runPlansArgs(t, "list", "--dir", root)
	if !strings.Contains(listOut, "1. feature-b") || !strings.Contains(listOut, "2. feature-a") {
		t.Fatalf("reorder did not persist:\n%s", listOut)
	}

	if out, err := runPlansArgs(t, "remove", "--dir", root, "--id", "feature-a"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	listOut, _ = runPlansArgs(t, "list", "--dir", root)
	if strings.Contains(listOut, "feature-a") {
		t.Fatalf("feature-a still present after remove:\n%s", listOut)
	}
}

func TestPlansAddRejectsMissingFile(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusConfig(t, root, []map[string]any{})

	out, err := runPlansArgs(t, "add", "--dir", root, "--id", "ghost", "--path", "ghost.md")
	if err == nil {
		t.Fatalf("expected error for missing plan file, got output:\n%s", out)
	}
}

func TestPlansAddRequiresFlags(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusConfig(t, root, []map[string]any{})

	if _, err := runPlansArgs(t, "add", "--dir", root, "--path", "feature.md"); err == nil {
		t.Fatalf("expected --id required error")
	}
	if _, err := runPlansArgs(t, "add", "--dir", root, "--id", "feature-a"); err == nil {
		t.Fatalf("expected --path required error")
	}
}

func TestPlansAddPersistsCanonicalPath(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{})

	if _, err := runPlansArgs(t, "add", "--dir", root, "--id", "feature-a", "--path", "feature.md"); err != nil {
		t.Fatalf("add: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	units := cfg["plan_units"].([]any)
	first := units[0].(map[string]any)
	if first["path"] != "springfield/plans/feature.md" {
		t.Fatalf("path not canonicalized: %v", first["path"])
	}
}

func TestPlansAddBootstrapsExecutionConfigWhenMissing(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")

	if _, err := os.Stat(filepath.Join(root, ".springfield", "execution", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no execution config before add, got %v", err)
	}

	if out, err := runPlansArgs(t, "add", "--dir", root, "--id", "feature-a", "--path", "feature.md"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, ".springfield", "execution", "config.json")); err != nil {
		t.Fatalf("execution config not bootstrapped: %v", err)
	}

	statusOut, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(statusOut, "feature-a") || !strings.Contains(statusOut, "Plan registry:") {
		t.Fatalf("status did not reflect new plan:\n%s", statusOut)
	}
}

func TestPlansAddMigratesLegacyPlansDir(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")

	// Simulate an upgraded repo whose existing config still uses the legacy
	// .springfield/execution/plans path.
	cfg := map[string]any{
		"plans_dir":                    ".springfield/execution/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"single_workstream_timeout":    600,
		"tool":                         "claude",
		"sequential":                   []string{},
		"batches":                      [][]string{},
	}
	writeStatusJSON(t, root, "execution/config.json", cfg)

	if out, err := runPlansArgs(t, "add", "--dir", root, "--id", "feature-a", "--path", "feature.md"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(root, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if got["plans_dir"] != "springfield/plans" {
		t.Fatalf("plans_dir not migrated: %v", got["plans_dir"])
	}
	units := got["plan_units"].([]any)
	first := units[0].(map[string]any)
	if first["path"] != "springfield/plans/feature.md" {
		t.Fatalf("path not canonicalized to tracked dir: %v", first["path"])
	}
}

func TestPlansAddDoesNotExposeRefOrBranchFlags(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")

	if _, err := runPlansArgs(t, "add", "--dir", root, "--id", "feature-a", "--path", "feature.md", "--ref", "main"); err == nil {
		t.Fatalf("expected unknown-flag error for --ref")
	}
	if _, err := runPlansArgs(t, "add", "--dir", root, "--id", "feature-a", "--path", "feature.md", "--plan-branch", "x"); err == nil {
		t.Fatalf("expected unknown-flag error for --plan-branch")
	}
}

func TestPlansRemoveRepairsMissingPlanFile(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")

	if _, err := runPlansArgs(t, "add", "--dir", root, "--id", "ghost", "--path", "feature.md"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Simulate the user deleting the plan file out from under Springfield.
	if err := os.Remove(filepath.Join(root, "springfield", "plans", "feature.md")); err != nil {
		t.Fatalf("rm plan file: %v", err)
	}

	// status now surfaces a concrete error rather than stale registry output.
	statusOut, statusErr := runStatusIn(root)
	if statusErr == nil {
		t.Fatalf("expected status error for missing plan file, got:\n%s", statusOut)
	}
	if !strings.Contains(statusErr.Error(), "file not found") {
		t.Fatalf("status error missing concrete cause: %v", statusErr)
	}

	// Repair path must work without hand-editing JSON.
	if out, err := runPlansArgs(t, "remove", "--dir", root, "--id", "ghost"); err != nil {
		t.Fatalf("remove (repair): %v\n%s", err, out)
	}

	listOut, err := runPlansArgs(t, "list", "--dir", root)
	if err != nil {
		t.Fatalf("list after repair: %v", err)
	}
	if strings.Contains(listOut, "ghost") {
		t.Fatalf("ghost plan still present after repair:\n%s", listOut)
	}

	// Status now back to a clean valid state.
	statusOut, err = runStatusIn(root)
	if err != nil {
		t.Fatalf("status after repair: %v", err)
	}
	if !strings.Contains(statusOut, "springfield plans add") {
		t.Fatalf("expected empty-registry hint after repair:\n%s", statusOut)
	}
}

func runPlansArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewPlansCommand()
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	return buf.String(), err
}
