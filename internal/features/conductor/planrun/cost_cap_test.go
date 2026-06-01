package planrun_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/cost"
)

// TestSinglePlanCostCapAbortsIterationLoop is the headline integration test
// the vendor-economics pivot was missing: a plan with an unpassed story that
// keeps running iterations must abort INSIDE the iteration loop as soon as
// the rollup crosses CostCapUSD, not "at the end of the plan."
//
// The fake runner emits a claude stream-json usage event sized to cost ~$3
// per iteration against the static pricing table for claude-sonnet-4-6
// (1M input tokens at $3/Mtok). Cap is $1.00 so iteration 1's $3.00 spend
// crosses on the first WriteCost call.
func TestSinglePlanCostCapAbortsIterationLoop(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	// Agent emits a usage event that the claude ExtractCost sums into 1M
	// input tokens. Pricing: $3.00 per iteration. No story-pass marker
	// means the loop would keep iterating if not for the cap.
	runner := &fakeAgentRunner{}
	runner.events = []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"assistant","message":{"usage":{"input_tokens":1000000,"output_tokens":0}}}`},
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		ProjectRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		ExecutionSettings: agents.ExecutionSettings{
			Claude: agents.ClaudeExecutionSettings{Model: "claude-sonnet-4-6"},
		},
		Runner:     runner,
		Manager:    &planrun.Manager{Git: g},
		CostCapUSD: 1.00,
	})
	if res.Err != nil {
		t.Fatalf("SinglePlan should not surface err on cost-cap: %v", res.Err)
	}
	if !res.CostCapped {
		t.Fatalf("expected CostCapped=true, got %+v", res)
	}
	if res.SpendUSD < 1.00 {
		t.Errorf("expected SpendUSD >= cap, got $%.2f", res.SpendUSD)
	}
	if !strings.Contains(res.Reason, "cost-capped") {
		t.Errorf("expected reason to mention cost-cap, got %q", res.Reason)
	}
	if res.Status != conductor.StatusInterrupted {
		t.Errorf("status=%v want StatusInterrupted (cap is resumable, not failure)", res.Status)
	}
	// The cap fires after the FIRST iteration writes its cost.json ($3 >= $1).
	// Exact-1 expected; anything else means the cap check is not where the
	// plan documents it (between WriteCost and the next iteration dispatch).
	if len(runner.calls) != 1 {
		t.Errorf("expected exactly 1 agent dispatch before cap; got %d", len(runner.calls))
	}

	// Verify cost.json landed under the live evidence path so ComputeRollup
	// will see the spend on resume.
	matches, _ := filepath.Glob(filepath.Join(root, ".springfield", "execution", "plans", "*", "evidence", "iter-*", "cost.json"))
	if len(matches) == 0 {
		t.Errorf("expected at least one cost.json on disk after cap fired")
	}

	// Persisted state must NOT be StatusCompleted (otherwise runBatch would
	// merge the plan).
	reloaded, _ := conductor.LoadProject(root)
	st := reloaded.State.Plans["alpha"]
	if st == nil {
		t.Fatal("no state persisted")
	}
	if st.Status == conductor.StatusCompleted {
		t.Error("cost-capped plan must not persist as completed; would trigger spurious merge")
	}
}

// TestSinglePlanStampsBatchIDOnCostJSON locks the write-side half of the
// batch-scoped rollup: a run given a BatchID must stamp every cost.json it
// writes with that id, so cost.ComputeRollup(root, batchID) sees this run's
// spend and excludes any other batch's leaked evidence. Without the stamp,
// a scoped cost-cap check would read $0 and never fire.
func TestSinglePlanStampsBatchIDOnCostJSON(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	g := newFakeGit()
	runner := &fakeAgentRunner{}
	runner.events = []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"assistant","message":{"usage":{"input_tokens":1000000,"output_tokens":0}}}`},
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		ProjectRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		ExecutionSettings: agents.ExecutionSettings{
			Claude: agents.ClaudeExecutionSettings{Model: "claude-sonnet-4-6"},
		},
		Runner:     runner,
		Manager:    &planrun.Manager{Git: g},
		CostCapUSD: 1.00,
		BatchID:    "batch-X",
	})
	if !res.CostCapped {
		t.Fatalf("expected cap to fire under its own batch's spend; got %+v", res)
	}

	// The written cost.json must carry batch_id=batch-X.
	matches, _ := filepath.Glob(filepath.Join(root, ".springfield", "execution", "plans", "*", "evidence", "iter-*", "cost.json"))
	if len(matches) == 0 {
		t.Fatal("expected a cost.json on disk")
	}
	data, _ := os.ReadFile(matches[0])
	var c cost.Capture
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("decode cost.json: %v", err)
	}
	if c.BatchID != "batch-X" {
		t.Errorf("cost.json batch_id=%q want %q", c.BatchID, "batch-X")
	}

	// A scoped rollup for batch-X sees the spend; a scoped rollup for a
	// different batch sees nothing (the leakage-exclusion guarantee).
	rX, _ := cost.ComputeRollup(root, "batch-X")
	if rX.TotalUSD < 1.00 {
		t.Errorf("scoped rollup for batch-X=$%.2f want >= cap $1.00", rX.TotalUSD)
	}
	rOther, _ := cost.ComputeRollup(root, "batch-OTHER")
	if rOther.TotalUSD != 0 {
		t.Errorf("scoped rollup for a different batch=$%.2f want $0 (no cross-contamination)", rOther.TotalUSD)
	}
}

// TestSinglePlanCostCapBoundaryExact verifies the >= semantics: when the
// rollup exactly equals the cap (to float precision), the abort fires.
// This guards against an off-by-one between > and >= that would otherwise
// be invisible until production.
func TestSinglePlanCostCapBoundaryExact(t *testing.T) {
	root := projectFixtureWithUnpassedStory(t, "alpha")
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	g := newFakeGit()
	// Pre-seed a cost.json under the live evidence path so ComputeRollup
	// returns exactly $1.00 BEFORE the first iteration runs. The first
	// iteration's WriteCost adds whatever the runner emits (zero usage
	// → $0 added) so the post-WriteCost rollup is exactly $1.00.
	planKey := planrun.PlanKey(conductor.PlanUnit{ID: "alpha"})
	preDir := filepath.Join(root, ".springfield", "execution", "plans", planKey, "evidence", "iter-pre")
	if err := os.MkdirAll(preDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	preCapture := cost.Capture{Adapter: "claude", Model: "claude-sonnet-4-6", CostUSD: 1.00}
	preData, _ := json.MarshalIndent(preCapture, "", "  ")
	if err := os.WriteFile(filepath.Join(preDir, "cost.json"), preData, 0o644); err != nil {
		t.Fatalf("write pre-seed: %v", err)
	}

	runner := &fakeAgentRunner{}
	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		ProjectRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		CostCapUSD:   1.00,
	})
	if !res.CostCapped {
		t.Errorf("equality with cap must fire (>=); got CostCapped=%v Reason=%q", res.CostCapped, res.Reason)
	}
}
