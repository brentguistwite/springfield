package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
)

// writePRDJSON writes a minimal prd.json under .springfield/plans/<planID>/prd.json.
// Used in tests where the conductor config points at a PRD-style path
// (writePlanFileBinary unconditionally appends .md, which is wrong here).
func writePRDJSON(t *testing.T, root, planID string) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "plans", planID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := `{"id":"` + planID + `","user_stories":[]}`
	if err := os.WriteFile(filepath.Join(dir, "prd.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write prd.json: %v", err)
	}
}

// writeActiveBatchBinaryN is the multi-plan variant of writeActiveBatchBinary.
// Each plan is its own serial phase, matching how `springfield plan` typically
// compiles batches that lack explicit phase grouping.
func writeActiveBatchBinaryN(t *testing.T, root, batchID, title string, planIDs []string) {
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

// integratedPlan returns a PlanState that IsIntegrated() reports true for.
// Mirrors the conditions enforced by conductor.PlanState.IsIntegrated:
// Status=Completed AND (Merge=nil OR Merge.Status=Succeeded with Cleanup=Succeeded).
func integratedPlan() *conductor.PlanState {
	return &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status:           conductor.MergeSucceeded,
			SourceSyncStatus: "synced",
		},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}
}

// TestStatusActiveBatchRollupShapeViaBinary is the binary-layer acceptance
// test for the new plan-centric rollup output. Mirrors the internal cmd test
// at TestStatusRollupOneInFlight but exercises the assembled CLI so that any
// future refactor that moves logic out of printBatchStatus is caught here
// instead of slipping through internal-only coverage.
func TestStatusActiveBatchRollupShapeViaBinary(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	// Register two plan units (required for LoadProjectRaw to see Plans state).
	plans := []conductor.PlanUnit{
		{ID: "01", Title: "Plan 01", Path: ".springfield/plans/01/prd.json", Order: 1},
		{ID: "02", Title: "Plan 02", Path: ".springfield/plans/02/prd.json", Order: 2},
	}
	for _, p := range plans {
		writePRDJSON(t, root, p.ID)
	}
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  plans,
	})

	// Active batch with two plans, plan 01 integrated, plan 02 running.
	writeActiveBatchBinaryN(t, root, "batch-001", "Active Batch", []string{"01", "02"})
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"01": integratedPlan(),
			"02": {Status: conductor.StatusRunning},
		},
	})

	output, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, output)
	}

	want := []string{
		"Batch: batch-001",
		"Plans: 1/2 integrated",
		"Current: 02 (running)",
	}
	for _, w := range want {
		if !strings.Contains(output, w) {
			t.Errorf("expected %q in status output:\n%s", w, output)
		}
	}
	if strings.Contains(output, "Phase: 1 of") {
		t.Errorf("stale 'Phase: 1 of N' line resurrected via the binary:\n%s", output)
	}
}

// TestStatusActiveBatchAllDoneViaBinary confirms the Status: complete branch
// renders end-to-end. Belt-and-suspenders for the AllDone path.
func TestStatusActiveBatchAllDoneViaBinary(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	plans := []conductor.PlanUnit{
		{ID: "01", Title: "Plan 01", Path: ".springfield/plans/01/prd.json", Order: 1},
	}
	writePRDJSON(t, root, "01")
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  plans,
	})

	writeActiveBatchBinaryN(t, root, "batch-001", "Active Batch", []string{"01"})
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"01": integratedPlan(),
		},
	})

	output, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Plans: 1/1 integrated") {
		t.Errorf("expected 'Plans: 1/1 integrated':\n%s", output)
	}
	if !strings.Contains(output, "Status: complete") {
		t.Errorf("expected 'Status: complete':\n%s", output)
	}
}
