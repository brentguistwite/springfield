package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/lock"
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
		{"id": "feature-a", "title": "Feature A", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "feature-b", "title": "Feature B", "path": ".springfield/plans/feature.md", "order": 2},
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
	if !strings.Contains(out, "springfield start") {
		t.Fatalf("plan-registry status should advertise springfield start in parity 2:\n%s", out)
	}
	if !strings.Contains(out, "worktree") {
		t.Fatalf("expected worktree-based execution mention:\n%s", out)
	}
}

func TestStatusActiveBatchWinsArbitration(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": ".springfield/plans/feature.md", "order": 1},
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

func TestStatusRollupNothingRunning(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Plans: 0/1 integrated") {
		t.Fatalf("expected 0/1 rollup:\n%s", out)
	}
	if !strings.Contains(out, "Next: 01") {
		t.Fatalf("expected Next pointer:\n%s", out)
	}
	if strings.Contains(out, "Phase:") {
		t.Fatalf("stale Phase: line leaked:\n%s", out)
	}
}

func TestStatusRollupOneInFlight(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "02", "path": ".springfield/plans/feature.md", "order": 2},
	})
	writeActiveBatchN(t, root, "batch-001", "Active Batch", []string{"01", "02"})
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"01": map[string]any{"status": "running"},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Plans: 0/2 integrated") {
		t.Fatalf("expected Plans: 0/2 integrated:\n%s", out)
	}
	if !strings.Contains(out, "Current: 01 (running)") {
		t.Fatalf("expected Current running line:\n%s", out)
	}
	if !strings.Contains(out, "Next: 02") {
		t.Fatalf("expected Next: 02:\n%s", out)
	}
}

func TestStatusRollupParallelInFlight(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "02", "path": ".springfield/plans/feature.md", "order": 2},
	})
	writeActiveBatchParallel(t, root, "batch-001", "Active Batch", []string{"01", "02"})
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"01": map[string]any{"status": "running"},
			"02": map[string]any{"status": "running"},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Current: 01, 02 (parallel)") {
		t.Fatalf("expected Current: 01, 02 (parallel) line:\n%s", out)
	}
}

func TestStatusRollupAllDone(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"01": map[string]any{
				"status": "completed",
				"merge": map[string]any{
					"status":             "succeeded",
					"source_sync_status": "synced",
				},
				"cleanup": map[string]any{"status": "succeeded"},
			},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Plans: 1/1 integrated") {
		t.Fatalf("expected 1/1 rollup:\n%s", out)
	}
	if !strings.Contains(out, "Status: complete") {
		t.Fatalf("expected Status: complete:\n%s", out)
	}
}

func TestStatusRollupDegradesWhenStateLoadFails(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")
	// Corrupt state.json so LoadProjectRaw fails.
	stateFile := filepath.Join(root, ".springfield", "execution", "state.json")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(stateFile, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status should degrade not error: %v", err)
	}
	if !strings.Contains(out, "Batch: batch-001") {
		t.Fatalf("expected batch header to still render:\n%s", out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Fatalf("expected stderr warn line:\n%s", out)
	}
	if strings.Contains(out, "Plans: ") && strings.Contains(out, "integrated") {
		t.Fatalf("rollup should be omitted when state load fails:\n%s", out)
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
		{"id": "feature-a", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "feature-b", "path": ".springfield/plans/feature.md", "order": 2},
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
}

func TestStatusRewritesStaleRunningPlanToInterruptedAndGuidesResume(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "feature-b", "path": ".springfield/plans/feature.md", "order": 2},
	})
	wt := filepath.Join(root, ".worktrees", "feature-a")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"feature-a": map[string]any{
				"status":        "running",
				"attempts":      1,
				"worktree_path": wt,
				"branch":        "springfield/feature-a",
				"base_ref":      "main",
				"base_head":     "aaaaaaaa",
			},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "feature-a  interrupted") {
		t.Fatalf("expected interrupted status:\n%s", out)
	}
	if !strings.Contains(out, "resume interrupted plan \"feature-a\"") {
		t.Fatalf("expected resume guidance:\n%s", out)
	}
	if !strings.Contains(out, "interrupted-process-exit") {
		t.Fatalf("expected interruption exit reason:\n%s", out)
	}

	stateBytes, err := os.ReadFile(filepath.Join(root, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(stateBytes), "\"status\": \"interrupted\"") {
		t.Fatalf("state not rewritten to interrupted:\n%s", stateBytes)
	}
}

func TestStatusDoesNotRewriteRunningPlanWhileStartLockHeld(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"feature-a": map[string]any{
				"status":        "running",
				"attempts":      1,
				"worktree_path": filepath.Join(root, ".worktrees", "feature-a"),
				"branch":        "springfield/feature-a",
				"base_ref":      "main",
				"base_head":     "aaaaaaaa",
			},
		},
	})

	lk, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lk.Release()

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "feature-a  running") {
		t.Fatalf("expected live running status:\n%s", out)
	}
	if !strings.Contains(out, "already running") {
		t.Fatalf("expected live-run guidance:\n%s", out)
	}

	stateBytes, err := os.ReadFile(filepath.Join(root, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(stateBytes), "\"status\": \"running\"") {
		t.Fatalf("state was mutated despite held lock:\n%s", stateBytes)
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
	dir := filepath.Join(root, ".springfield", "plans")
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
		"plans_dir":                    ".springfield/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"single_workstream_timeout":    600,
		"tool":                         "claude",
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
	writeActiveBatchN(t, root, batchID, title, []string{"01"})
}

func writeActiveBatchN(t *testing.T, root, batchID, title string, planIDs []string) {
	t.Helper()
	paths, err := batch.NewPaths(root, batchID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	phases := make([]batch.Phase, len(planIDs))
	for i, id := range planIDs {
		phases[i] = batch.Phase{Mode: batch.PhaseSerial, Plans: []string{id}}
	}
	b := batch.Batch{ID: batchID, Title: title, Phases: phases, PlanIDs: planIDs}
	if err := batch.WriteBatch(paths, b, "source", nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: batchID}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
}

func writeActiveBatchParallel(t *testing.T, root, batchID, title string, planIDs []string) {
	t.Helper()
	paths, err := batch.NewPaths(root, batchID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	b := batch.Batch{
		ID:      batchID,
		Title:   title,
		Phases:  []batch.Phase{{Mode: batch.PhaseParallel, Plans: planIDs}},
		PlanIDs: planIDs,
	}
	if err := batch.WriteBatch(paths, b, "source", nil); err != nil {
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
