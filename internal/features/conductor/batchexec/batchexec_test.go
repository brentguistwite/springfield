package batchexec_test

import (
	"context"
	"errors"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor/batchexec"
)

// fakeRunner is a scripted PlanRunner. Each RunPlan call records the plan ID
// and returns the scripted outcome (zero Outcome when unscripted). Terminal
// plans are skipped by Execute and marked terminal on successful settle.
type fakeRunner struct {
	dispatched []string
	outcomes   map[string]batchexec.Outcome
	terminal   map[string]bool
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{outcomes: map[string]batchexec.Outcome{}, terminal: map[string]bool{}}
}

func (f *fakeRunner) RunPlan(_ context.Context, planID string, _ batchexec.RunInfo) batchexec.Outcome {
	f.dispatched = append(f.dispatched, planID)
	out := f.outcomes[planID]
	if out.Err == nil && !out.CostCapped && !out.NoEligiblePlan {
		f.terminal[planID] = true
	}
	return out
}

func (f *fakeRunner) IsTerminal(planID string) bool { return f.terminal[planID] }

func twoPhaseBatch() batch.Batch {
	return batch.Batch{
		ID: "b1",
		Phases: []batch.Phase{
			{Mode: batch.PhaseSerial, Plans: []string{"p1", "p2"}},
			{Mode: batch.PhaseSerial, Plans: []string{"p3"}},
		},
		PlanIDs: []string{"p1", "p2", "p3"},
	}
}

func TestExecuteDispatchesAllPlansInPhaseOrder(t *testing.T) {
	r := newFakeRunner()
	res, err := batchexec.Execute(context.Background(), batchexec.Input{
		Batch:  twoPhaseBatch(),
		Runner: r,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.CostCapped {
		t.Fatalf("unexpected cost-cap result: %+v", res)
	}
	want := []string{"p1", "p2", "p3"}
	if got := r.dispatched; len(got) != len(want) || got[0] != "p1" || got[1] != "p2" || got[2] != "p3" {
		t.Fatalf("dispatch order = %v, want %v", got, want)
	}
}

func TestExecuteSkipsAlreadyIntegratedPlans(t *testing.T) {
	r := newFakeRunner()
	r.terminal["p1"] = true // resume: p1 integrated in a prior run
	_, err := batchexec.Execute(context.Background(), batchexec.Input{Batch: twoPhaseBatch(), Runner: r})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := r.dispatched; len(got) != 2 || got[0] != "p2" || got[1] != "p3" {
		t.Fatalf("dispatch order = %v, want [p2 p3]", got)
	}
}

func TestExecuteHaltsOnPlanFailure(t *testing.T) {
	r := newFakeRunner()
	wantErr := errors.New("boom")
	r.outcomes["p2"] = batchexec.Outcome{Err: wantErr}
	_, err := batchexec.Execute(context.Background(), batchexec.Input{Batch: twoPhaseBatch(), Runner: r})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	// p3 (next phase) must not have been dispatched.
	if got := r.dispatched; len(got) != 2 || got[1] != "p2" {
		t.Fatalf("dispatch order = %v, want [p1 p2]", got)
	}
}

func TestExecuteReportsNeedsHumanAlongsideHaltError(t *testing.T) {
	r := newFakeRunner()
	wantErr := errors.New("plan paused for review")
	r.outcomes["p2"] = batchexec.Outcome{Err: wantErr, NeedsHuman: true}
	res, err := batchexec.Execute(context.Background(), batchexec.Input{Batch: twoPhaseBatch(), Runner: r})
	// Needs-human still halts the batch (Err surfaces), but the batch-level
	// result must flag it so the caller can notify needs-human, not failure.
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if !res.NeedsHuman {
		t.Fatalf("result = %+v, want NeedsHuman", res)
	}
}

// A genuine failure in the same phase outranks a needs-human pause: the more
// urgent failure must drive the batch-level report, so NeedsHuman is cleared.
func TestExecuteHardFailureOutranksNeedsHuman(t *testing.T) {
	r := newFakeRunner()
	// Parallel phase so both plans settle before the barrier: p1 needs human,
	// p2 hard-fails.
	b := batch.Batch{
		ID:      "b1",
		Phases:  []batch.Phase{{Mode: batch.PhaseParallel, Plans: []string{"p1", "p2"}}},
		PlanIDs: []string{"p1", "p2"},
	}
	r.outcomes["p1"] = batchexec.Outcome{Err: errors.New("needs review"), NeedsHuman: true}
	r.outcomes["p2"] = batchexec.Outcome{Err: errors.New("hard fail")}
	res, err := batchexec.Execute(context.Background(), batchexec.Input{
		Batch: b, Runner: r, Parallelize: true, MaxParallel: 2,
	})
	if err == nil {
		t.Fatalf("expected halt error, got nil")
	}
	if res.NeedsHuman {
		t.Fatalf("result = %+v, want NeedsHuman cleared by hard failure", res)
	}
}

func TestExecuteReturnsCostCapPauseWithoutError(t *testing.T) {
	r := newFakeRunner()
	r.outcomes["p2"] = batchexec.Outcome{CostCapped: true, SpendUSD: 12.5}
	res, err := batchexec.Execute(context.Background(), batchexec.Input{Batch: twoPhaseBatch(), Runner: r})
	if err != nil {
		t.Fatalf("cost-cap must pause, not fail: %v", err)
	}
	if !res.CostCapped || res.SpendUSD != 12.5 {
		t.Fatalf("result = %+v, want CostCapped with SpendUSD 12.5", res)
	}
	if got := r.dispatched; len(got) != 2 {
		t.Fatalf("dispatched %v after cost-cap, want stop at p2", got)
	}
}

func TestExecuteStopsCleanlyOnNoEligiblePlan(t *testing.T) {
	r := newFakeRunner()
	r.outcomes["p1"] = batchexec.Outcome{NoEligiblePlan: true}
	res, err := batchexec.Execute(context.Background(), batchexec.Input{Batch: twoPhaseBatch(), Runner: r})
	if err != nil || res.CostCapped {
		t.Fatalf("no-eligible-plan must stop cleanly: res=%+v err=%v", res, err)
	}
	if len(r.dispatched) != 1 {
		t.Fatalf("dispatched %v, want stop after p1", r.dispatched)
	}
}

func TestExecuteReturnsCtxErrWhenPreCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := newFakeRunner()
	res, err := batchexec.Execute(ctx, batchexec.Input{Batch: twoPhaseBatch(), Runner: r})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !res.Cancelled {
		t.Fatalf("result = %+v, want Cancelled", res)
	}
	if len(r.dispatched) != 0 {
		t.Fatalf("dispatched %v under pre-cancelled ctx, want none", r.dispatched)
	}
}

// cancellingRunner cancels the shared ctx while "running" its first plan,
// simulating a signal arriving mid-plan.
type cancellingRunner struct {
	*fakeRunner
	cancel context.CancelFunc
}

func (c *cancellingRunner) RunPlan(ctx context.Context, planID string, info batchexec.RunInfo) batchexec.Outcome {
	c.cancel()
	return c.fakeRunner.RunPlan(ctx, planID, info)
}

func TestExecuteStopsDispatchAfterMidRunCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &cancellingRunner{fakeRunner: newFakeRunner(), cancel: cancel}
	_, err := batchexec.Execute(ctx, batchexec.Input{Batch: twoPhaseBatch(), Runner: r})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(r.dispatched) != 1 {
		t.Fatalf("dispatched %v, want exactly [p1] before cancellation observed", r.dispatched)
	}
}
