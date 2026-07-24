package batchexec_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor/batchexec"
)

// blockingRunner lets tests control exactly when each plan settles: RunPlan
// signals start via started[id], then blocks until the test sends the outcome
// on release[id]. This makes interleavings deterministic without sleeps on
// the happy path.
type blockingRunner struct {
	mu       sync.Mutex
	terminal map[string]bool
	started  map[string]chan struct{}
	release  map[string]chan batchexec.Outcome
	runInfo  map[string]batchexec.RunInfo
}

func newBlockingRunner(planIDs ...string) *blockingRunner {
	r := &blockingRunner{
		terminal: map[string]bool{},
		started:  map[string]chan struct{}{},
		release:  map[string]chan batchexec.Outcome{},
		runInfo:  map[string]batchexec.RunInfo{},
	}
	for _, id := range planIDs {
		r.started[id] = make(chan struct{})
		r.release[id] = make(chan batchexec.Outcome, 1)
	}
	return r
}

func (r *blockingRunner) RunPlan(_ context.Context, planID string, info batchexec.RunInfo) batchexec.Outcome {
	r.mu.Lock()
	r.runInfo[planID] = info
	r.mu.Unlock()
	close(r.started[planID])
	out := <-r.release[planID]
	if out.Err == nil && !out.CostCapped && !out.NoEligiblePlan {
		r.mu.Lock()
		r.terminal[planID] = true
		r.mu.Unlock()
	}
	return out
}

func (r *blockingRunner) IsTerminal(planID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terminal[planID]
}

// waitStarted fails the test unless the plan starts within the deadline.
func (r *blockingRunner) waitStarted(t *testing.T, planID string) {
	t.Helper()
	select {
	case <-r.started[planID]:
	case <-time.After(5 * time.Second):
		t.Fatalf("plan %s never started", planID)
	}
}

// assertNotStarted asserts the plan has not started. A short settle window
// lets any wrongly-spawned goroutine reach its start signal first.
func (r *blockingRunner) assertNotStarted(t *testing.T, planID string) {
	t.Helper()
	select {
	case <-r.started[planID]:
		t.Fatalf("plan %s started but must not have", planID)
	case <-time.After(50 * time.Millisecond):
	}
}

func parallelBatch(mode batch.PhaseMode, phases ...[]string) batch.Batch {
	b := batch.Batch{ID: "b1"}
	for _, plans := range phases {
		b.Phases = append(b.Phases, batch.Phase{Mode: mode, Plans: plans})
		b.PlanIDs = append(b.PlanIDs, plans...)
	}
	return b
}

// runExecute runs Execute in a goroutine and returns a channel with its result.
type executeResult struct {
	res batchexec.Result
	err error
}

func runExecute(in batchexec.Input) chan executeResult {
	done := make(chan executeResult, 1)
	go func() {
		res, err := batchexec.Execute(context.Background(), in)
		done <- executeResult{res, err}
	}()
	return done
}

func waitExecute(t *testing.T, done chan executeResult) executeResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Execute never returned")
		return executeResult{}
	}
}

func TestParallelPhaseRespectsCapAndCompletesAll(t *testing.T) {
	r := newBlockingRunner("p1", "p2", "p3")
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseParallel, []string{"p1", "p2", "p3"}),
		Runner:      r,
		Parallelize: true,
		MaxParallel: 2,
	})

	r.waitStarted(t, "p1")
	r.waitStarted(t, "p2")
	// Cap is 2: p3 must not start while p1 and p2 are both in flight.
	r.assertNotStarted(t, "p3")

	r.release["p1"] <- batchexec.Outcome{}
	// A free slot: p3 starts.
	r.waitStarted(t, "p3")
	r.release["p2"] <- batchexec.Outcome{}
	r.release["p3"] <- batchexec.Outcome{}

	out := waitExecute(t, done)
	if out.err != nil || out.res.CostCapped {
		t.Fatalf("Execute = %+v, %v; want clean completion", out.res, out.err)
	}
	// RunInfo.Concurrent is phase-static: every plan of the parallel phase
	// reports concurrent — including ones dispatched after siblings settled.
	for _, id := range []string{"p1", "p2", "p3"} {
		if !r.runInfo[id].Concurrent {
			t.Errorf("plan %s: RunInfo.Concurrent = false, want true (phase-static)", id)
		}
	}
}

func TestPhaseBarrierHoldsUnderParallelism(t *testing.T) {
	r := newBlockingRunner("p1", "p2", "p3")
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseParallel, []string{"p1", "p2"}, []string{"p3"}),
		Runner:      r,
		Parallelize: true,
		MaxParallel: 3,
	})

	r.waitStarted(t, "p1")
	r.waitStarted(t, "p2")
	r.release["p1"] <- batchexec.Outcome{}
	// p2 still in flight: the next phase must not start.
	r.assertNotStarted(t, "p3")
	r.release["p2"] <- batchexec.Outcome{}
	r.waitStarted(t, "p3")
	r.release["p3"] <- batchexec.Outcome{}

	out := waitExecute(t, done)
	if out.err != nil {
		t.Fatalf("Execute error: %v", out.err)
	}
}

func TestParallelPhaseFailureDrainsPhaseThenHalts(t *testing.T) {
	r := newBlockingRunner("p1", "p2", "p3", "p4")
	boom := errors.New("p1 boom")
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseParallel, []string{"p1", "p2", "p3"}, []string{"p4"}),
		Runner:      r,
		Parallelize: true,
		MaxParallel: 2,
	})

	r.waitStarted(t, "p1")
	r.waitStarted(t, "p2")
	r.release["p1"] <- batchexec.Outcome{Err: boom}
	// Independent sibling p3 must still be dispatched after p1's failure.
	r.waitStarted(t, "p3")
	r.release["p2"] <- batchexec.Outcome{}
	r.release["p3"] <- batchexec.Outcome{}

	out := waitExecute(t, done)
	if !errors.Is(out.err, boom) {
		t.Fatalf("err = %v, want %v", out.err, boom)
	}
	// Barrier: next phase never starts after a failed phase.
	r.assertNotStarted(t, "p4")
}

func TestParallelPhaseJoinsMultipleFailures(t *testing.T) {
	r := newBlockingRunner("p1", "p2")
	err1, err2 := errors.New("e1"), errors.New("e2")
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseParallel, []string{"p1", "p2"}),
		Runner:      r,
		Parallelize: true,
		MaxParallel: 2,
	})
	r.waitStarted(t, "p1")
	r.waitStarted(t, "p2")
	r.release["p1"] <- batchexec.Outcome{Err: err1}
	r.release["p2"] <- batchexec.Outcome{Err: err2}
	out := waitExecute(t, done)
	if !errors.Is(out.err, err1) || !errors.Is(out.err, err2) {
		t.Fatalf("err = %v, want both %v and %v", out.err, err1, err2)
	}
}

func TestSerialPhaseKeepsImmediateHaltEvenWhenParallelizeEnabled(t *testing.T) {
	r := newBlockingRunner("p1", "p2")
	boom := errors.New("boom")
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseSerial, []string{"p1", "p2"}),
		Runner:      r,
		Parallelize: true,
		MaxParallel: 3,
	})
	r.waitStarted(t, "p1")
	r.release["p1"] <- batchexec.Outcome{Err: boom}
	out := waitExecute(t, done)
	if !errors.Is(out.err, boom) {
		t.Fatalf("err = %v, want %v", out.err, boom)
	}
	// Serial semantics: the failure halts before p2 is ever dispatched.
	r.assertNotStarted(t, "p2")
}

func TestCostCapStopsDispatchImmediatelyAndDrains(t *testing.T) {
	r := newBlockingRunner("p1", "p2", "p3")
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseParallel, []string{"p1", "p2", "p3"}),
		Runner:      r,
		Parallelize: true,
		MaxParallel: 2,
	})
	r.waitStarted(t, "p1")
	r.waitStarted(t, "p2")
	r.release["p1"] <- batchexec.Outcome{CostCapped: true, SpendUSD: 5}
	// Pause: p3 must NOT be dispatched even though a slot freed up.
	r.assertNotStarted(t, "p3")
	// In-flight sibling drains before Execute returns.
	r.release["p2"] <- batchexec.Outcome{}
	out := waitExecute(t, done)
	if out.err != nil || !out.res.CostCapped || out.res.SpendUSD != 5 {
		t.Fatalf("Execute = %+v, %v; want cost-cap pause with spend 5", out.res, out.err)
	}
}

func TestCostCapCoexistsWithSiblingFailure(t *testing.T) {
	r := newBlockingRunner("p1", "p2")
	boom := errors.New("p2 boom")
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseParallel, []string{"p1", "p2"}),
		Runner:      r,
		Parallelize: true,
		MaxParallel: 2,
	})
	r.waitStarted(t, "p1")
	r.waitStarted(t, "p2")
	// Cap fires on p1, then the draining sibling p2 fails: neither signal may
	// eclipse the other — the caller needs the error for the failure report
	// AND the cap pause (with spend) for the resume flow.
	r.release["p1"] <- batchexec.Outcome{CostCapped: true, SpendUSD: 7}
	r.release["p2"] <- batchexec.Outcome{Err: boom}
	out := waitExecute(t, done)
	if !errors.Is(out.err, boom) {
		t.Fatalf("err = %v, want %v", out.err, boom)
	}
	if !out.res.CostCapped || out.res.SpendUSD != 7 {
		t.Fatalf("res = %+v, want CostCapped with spend 7 alongside the error", out.res)
	}
}

func TestParallelPhaseRunsSequentiallyWhenParallelizeDisabled(t *testing.T) {
	r := newBlockingRunner("p1", "p2")
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseParallel, []string{"p1", "p2"}),
		Runner:      r,
		Parallelize: false, // e.g. consolidate mode
		MaxParallel: 3,
	})
	r.waitStarted(t, "p1")
	// Without Parallelize, a parallel-mode phase still runs one at a time.
	r.assertNotStarted(t, "p2")
	r.release["p1"] <- batchexec.Outcome{}
	r.waitStarted(t, "p2")
	r.release["p2"] <- batchexec.Outcome{}
	out := waitExecute(t, done)
	if out.err != nil {
		t.Fatalf("Execute error: %v", out.err)
	}
	// Sequential execution of a parallel-mode phase is NOT concurrent.
	if r.runInfo["p1"].Concurrent || r.runInfo["p2"].Concurrent {
		t.Errorf("RunInfo.Concurrent must be false when Parallelize is disabled")
	}
}

func TestDispatchAndSettleCallbacksFireFromSchedulerGoroutine(t *testing.T) {
	r := newBlockingRunner("p1", "p2", "p3")
	// No mutex on purpose: -race verifies the callbacks are confined to one
	// goroutine (the scheduler), per the Input contract.
	var dispatched, settled []string
	done := runExecute(batchexec.Input{
		Batch:       parallelBatch(batch.PhaseParallel, []string{"p1", "p2", "p3"}),
		Runner:      r,
		Parallelize: true,
		MaxParallel: 3,
		OnDispatch:  func(id string) { dispatched = append(dispatched, id) },
		OnSettle:    func(id string) { settled = append(settled, id) },
	})
	for _, id := range []string{"p1", "p2", "p3"} {
		r.waitStarted(t, id)
		r.release[id] <- batchexec.Outcome{}
	}
	out := waitExecute(t, done)
	if out.err != nil {
		t.Fatalf("Execute error: %v", out.err)
	}
	if len(dispatched) != 3 || len(settled) != 3 {
		t.Fatalf("dispatched=%v settled=%v, want 3 each", dispatched, settled)
	}
}
