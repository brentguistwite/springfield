// Package batchexec owns batch execution order: it walks a batch's phases and
// dispatches plans to a PlanRunner, honoring phase barriers. It knows nothing
// about agents, git, config files, or how a plan actually runs — the runner
// closure supplied by the caller carries all of that.
//
// Concurrency model: phases execute strictly in order and a phase must fully
// settle before the next starts (the barrier). Within a phase marked
// batch.PhaseParallel — and only when Input.Parallelize is set — up to
// Input.MaxParallel plans run concurrently; every other phase dispatches one
// plan at a time in declared order, exactly as before this package existed.
package batchexec

import (
	"context"
	"errors"

	"springfield/internal/features/batch"
)

// Outcome is what running one plan produced, reduced to the signals the
// executor needs for its dispatch/halt decisions.
type Outcome struct {
	// NoEligiblePlan reports that the runner's schedule refused the plan
	// (not registered or already terminal there). The batch stops cleanly.
	NoEligiblePlan bool
	// CostCapped reports the batch-wide cost cap fired inside this plan.
	CostCapped bool
	// SpendUSD is the batch spend observed when CostCapped fired.
	SpendUSD float64
	// Err is a terminal plan failure (including failed integration).
	Err error
}

// RunInfo carries per-dispatch execution context to the runner.
type RunInfo struct {
	// Concurrent is phase-static: true for every plan of a phase that is
	// declared parallel, running with parallelism active, and containing
	// more than one plan — regardless of how many happen to be in flight at
	// any instant. Runners use it for stable presentation decisions (e.g.
	// prefixing log lines for the whole phase, never flipping mid-stream).
	Concurrent bool
}

// PlanRunner runs a single plan end-to-end (execution + integration).
//
// IsTerminal must return true only for FULLY INTEGRATED plans. A plan that
// completed execution but is not yet integrated (crash between execution and
// integration) is NOT terminal: it must still be dispatched, and RunPlan is
// responsible for detecting that state and shortcutting to integration only.
//
// When plans run concurrently, RunPlan is called from multiple goroutines;
// IsTerminal and the Input callbacks are only ever called from Execute's own
// scheduling goroutine.
type PlanRunner interface {
	RunPlan(ctx context.Context, planID string, info RunInfo) Outcome
	IsTerminal(planID string) bool
}

// Input configures one Execute call.
type Input struct {
	Batch  batch.Batch
	Runner PlanRunner
	// Parallelize enables concurrent dispatch inside batch.PhaseParallel
	// phases. Callers set it only when the batch's output mode makes plans
	// independent (per-plan branches).
	Parallelize bool
	// MaxParallel caps concurrently running plans within a parallel phase.
	// Values < 2 (including zero) mean sequential execution everywhere.
	MaxParallel int
	// OnDispatch/OnSettle, when non-nil, are invoked from the scheduling
	// goroutine as each plan starts/finishes — the hook for runtime cursor
	// checkpoints (run.json active_plan_ids).
	OnDispatch func(planID string)
	OnSettle   func(planID string)
}

// Result is the batch-level outcome of Execute.
type Result struct {
	// CostCapped mirrors Outcome.CostCapped at the batch level: the run is
	// paused, not failed.
	CostCapped bool
	SpendUSD   float64
	// Cancelled reports the batch stopped because ctx was cancelled (the
	// accompanying error is ctx.Err()). Distinguishes a cancellation from a
	// plan failure so callers can keep the two report shapes separate.
	Cancelled bool
}

// Execute walks the batch's phases in order and dispatches every
// non-terminal plan, honoring the phase barrier. Returns the first plan
// error (halting the batch at the current phase), a cost-cap pause, or
// ctx.Err() when cancelled.
func Execute(ctx context.Context, in Input) (Result, error) {
	for _, phase := range in.Batch.Phases {
		res, err, stop := runPhase(ctx, phase, in)
		if stop {
			return res, err
		}
	}
	return Result{}, nil
}

// planSettle pairs a finished plan with its outcome.
type planSettle struct {
	planID string
	out    Outcome
}

// runPhase drives one phase to completion. stop=true means the batch must
// not advance to the next phase (error, pause, clean stop, or cancellation).
func runPhase(ctx context.Context, phase batch.Phase, in Input) (Result, error, bool) {
	parallelActive := phase.Mode == batch.PhaseParallel && in.Parallelize && in.MaxParallel > 1
	maxInFlight := 1
	if parallelActive {
		maxInFlight = in.MaxParallel
	}
	// Phase-static by design: decided once from the declared phase, so a
	// plan's presentation can't flip when siblings start or finish.
	info := RunInfo{Concurrent: parallelActive && len(phase.Plans) > 1}

	pending := make([]string, 0, len(phase.Plans))
	for _, id := range phase.Plans {
		if !in.Runner.IsTerminal(id) {
			pending = append(pending, id)
		}
	}

	var (
		settles      = make(chan planSettle, len(pending))
		inFlight     = 0
		next         = 0
		errs         []error
		costCapped   bool
		spendUSD     float64
		noEligible   bool
		stopDispatch = false
	)

	for {
		// Dispatch while there is capacity and no stop condition. Ctx is
		// checked before every dispatch so a pre-cancelled context never
		// dispatches (preserving the historical early-exit path).
		for !stopDispatch && ctx.Err() == nil && next < len(pending) && inFlight < maxInFlight {
			id := pending[next]
			next++
			if in.OnDispatch != nil {
				in.OnDispatch(id)
			}
			inFlight++
			go func(planID string) {
				settles <- planSettle{planID: planID, out: in.Runner.RunPlan(ctx, planID, info)}
			}(id)
		}
		if inFlight == 0 {
			break
		}
		s := <-settles
		inFlight--
		if in.OnSettle != nil {
			in.OnSettle(s.planID)
		}
		switch {
		case s.out.NoEligiblePlan:
			// Schedule refused the plan: stop the batch cleanly, but let
			// any in-flight siblings drain first.
			noEligible = true
			stopDispatch = true
		case s.out.CostCapped:
			// Batch-level pause: stop dispatching immediately and drain.
			costCapped = true
			if s.out.SpendUSD > spendUSD {
				spendUSD = s.out.SpendUSD
			}
			stopDispatch = true
		case s.out.Err != nil:
			errs = append(errs, s.out.Err)
			// Parallel phases drain: independent siblings keep dispatching
			// and the batch halts at the barrier. Serial phases keep the
			// historical immediate halt.
			if !parallelActive {
				stopDispatch = true
			}
		}
	}

	switch {
	case len(errs) == 1:
		return Result{}, errs[0], true
	case len(errs) > 1:
		return Result{}, errors.Join(errs...), true
	case costCapped:
		return Result{CostCapped: true, SpendUSD: spendUSD}, nil, true
	case noEligible:
		return Result{}, nil, true
	case ctx.Err() != nil:
		return Result{Cancelled: true}, ctx.Err(), true
	}
	return Result{}, nil, false
}
