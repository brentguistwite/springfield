package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

func TestStatusNoConfigPointsAtRegistrationFlow(t *testing.T) {
	root := newStatusRoot(t)

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "springfield init") || !strings.Contains(out, "springfield plans add") {
		t.Fatalf("expected init+plans add hint, got:\n%s", out)
	}
	if strings.Contains(out, "springfield plan\"") {
		t.Fatalf("stale \"springfield plan\" hint leaked:\n%s", out)
	}
}

func TestStatusEmptyPlanRegistryPointsAtPlansAdd(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusConfig(t, root, []map[string]any{})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "springfield plans add") {
		t.Fatalf("expected plans add hint, got:\n%s", out)
	}
}

func TestStatusPlanRegistryWhenNoBatch(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "title": "Feature A", "path": "springfield/plans/feature.md", "order": 1},
		{"id": "feature-b", "title": "Feature B", "path": "springfield/plans/feature.md", "order": 2},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Plan registry:") {
		t.Fatalf("expected plan registry header:\n%s", out)
	}
	if !strings.Contains(out, "feature-a") || !strings.Contains(out, "feature-b") {
		t.Fatalf("missing plan ids:\n%s", out)
	}
	if strings.Contains(out, "springfield start") {
		t.Fatalf("plan-registry status must not advertise springfield start in slice 1:\n%s", out)
	}
	if !strings.Contains(out, "does not execute registered plans yet") {
		t.Fatalf("expected truthful slice-1 next-step:\n%s", out)
	}
}

func TestStatusActiveBatchWinsArbitration(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": "springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Batch: batch-001") {
		t.Fatalf("expected batch header:\n%s", out)
	}
	if strings.Contains(out, "Plan registry:") {
		t.Fatalf("plan registry leaked into active-batch output:\n%s", out)
	}
}

func TestStatusOrphanedBatchKeepsRecoveryGuidance(t *testing.T) {
	root := newStatusRoot(t)
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "ghost-batch"}); err != nil {
		t.Fatalf("write run: %v", err)
	}

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "orphaned") {
		t.Fatalf("expected orphan guidance:\n%s", out)
	}
	if !strings.Contains(out, "springfield recover") {
		t.Fatalf("expected recover hint:\n%s", out)
	}
}

func TestStatusReportsCompletedAndFailedTruthfully(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": "springfield/plans/feature.md", "order": 1},
		{"id": "feature-b", "path": "springfield/plans/feature.md", "order": 2},
	})
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"feature-a": map[string]any{"status": "completed"},
			"feature-b": map[string]any{"status": "failed", "error": "boom"},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "feature-a  completed") {
		t.Fatalf("expected feature-a completed:\n%s", out)
	}
	if !strings.Contains(out, "feature-b  failed") {
		t.Fatalf("expected feature-b failed:\n%s", out)
	}
	if strings.Contains(out, "springfield start") {
		t.Fatalf("status must not advertise springfield start with failures in slice 1:\n%s", out)
	}
}

func TestStatusRendersLegacySequentialBatchesWhenPlanUnitsEmpty(t *testing.T) {
	root := newStatusRoot(t)
	cfg := map[string]any{
		"plans_dir":                    "springfield/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"single_workstream_timeout":    600,
		"tool":                         "claude",
		"sequential":                   []string{"legacy-a"},
		"batches":                      [][]string{{"legacy-b"}},
	}
	writeStatusJSON(t, root, "execution/config.json", cfg)

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(out, "No plans configured") {
		t.Fatalf("legacy plans treated as empty:\n%s", out)
	}
	if !strings.Contains(out, "legacy-a") || !strings.Contains(out, "legacy-b") {
		t.Fatalf("missing legacy plan names:\n%s", out)
	}
	if !strings.Contains(out, "Legacy sequential/batches") {
		t.Fatalf("expected legacy section header:\n%s", out)
	}
	if strings.Contains(out, "springfield start") {
		t.Fatalf("legacy status must not advertise springfield start in slice 1:\n%s", out)
	}
}

// --- helpers ---

func newStatusRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	body := `[project]
agent_priority = ["claude"]
`
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	return root
}

func writeStatusPlan(t *testing.T, root, file string) {
	t.Helper()
	dir := filepath.Join(root, "springfield", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("# plan"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
}

func writeStatusConfig(t *testing.T, root string, planUnits []map[string]any) {
	t.Helper()
	cfg := map[string]any{
		"plans_dir":                    "springfield/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"single_workstream_timeout":    600,
		"tool":                         "claude",
		"batches":                      [][]string{},
		"sequential":                   []string{},
		"plan_units":                   planUnits,
	}
	writeStatusJSON(t, root, "execution/config.json", cfg)
}

func writeStatusState(t *testing.T, root string, state map[string]any) {
	t.Helper()
	writeStatusJSON(t, root, "execution/state.json", state)
}

func writeStatusJSON(t *testing.T, root, rel string, value any) {
	t.Helper()
	full := filepath.Join(root, ".springfield", rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", full, err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func writeActiveBatch(t *testing.T, root, batchID, title string) {
	t.Helper()
	paths, err := batch.NewPaths(root, batchID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	b := batch.Batch{
		ID:    batchID,
		Title: title,
		Phases: []batch.Phase{{Mode: batch.PhaseSerial, Slices: []string{"01"}}},
		Slices: []batch.Slice{{ID: "01", Title: "First", Status: batch.SliceQueued}},
	}
	if err := batch.WriteBatch(paths, b, "source"); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: batchID}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
}

func runStatusIn(root string) (string, error) {
	cmd := NewStatusCommand()
	cmd.SetArgs([]string{"--dir", root})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}
