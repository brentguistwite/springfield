// Package batchexec owns batch execution order: it walks a batch's phases and
// dispatches plans to a PlanRunner, honoring phase barriers. It knows nothing
// about agents, git, config files, or how a plan actually runs — the runner
// closure supplied by the caller carries all of that.
package batchexec

import (
	"context"

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

// PlanRunner runs a single plan end-to-end (execution + integration).
//
// IsTerminal must return true only for FULLY INTEGRATED plans. A plan that
// completed execution but is not yet integrated (crash between execution and
// integration) is NOT terminal: it must still be dispatched, and RunPlan is
// responsible for detecting that state and shortcutting to integration only.
type PlanRunner interface {
	RunPlan(ctx context.Context, planID string) Outcome
	IsTerminal(planID string) bool
}

// Input configures one Execute call.
type Input struct {
	Batch  batch.Batch
	Runner PlanRunner
}

// Result is the batch-level outcome of Execute.
type Result struct {
	// CostCapped mirrors Outcome.CostCapped at the batch level: the run is
	// paused, not failed.
	CostCapped bool
	SpendUSD   float64
	// Cancelled reports the batch stopped because ctx was cancelled between
	// dispatches (the accompanying error is ctx.Err()). Distinguishes a
	// cancellation from a plan failure so callers can keep the two report
	// shapes separate.
	Cancelled bool
}

// Execute walks the batch's phases in order and dispatches every
// non-terminal plan. A phase must fully settle before the next starts.
// Returns the first plan error (halting the batch), a cost-cap pause, or
// ctx.Err() when cancelled between dispatches.
func Execute(ctx context.Context, in Input) (Result, error) {
	for {
		if ctx.Err() != nil {
			return Result{Cancelled: true}, ctx.Err()
		}
		planID := nextPlanID(in.Batch, in.Runner)
		if planID == "" {
			return Result{}, nil
		}
		out := in.Runner.RunPlan(ctx, planID)
		switch {
		case out.NoEligiblePlan:
			return Result{}, nil
		case out.CostCapped:
			return Result{CostCapped: true, SpendUSD: out.SpendUSD}, nil
		case out.Err != nil:
			return Result{}, out.Err
		}
	}
}

// nextPlanID returns the first non-terminal plan in phase order, or "" when
// every plan is terminal (batch done). All plans in phase N must be terminal
// before any plan in phase N+1 is dispatched.
func nextPlanID(b batch.Batch, r PlanRunner) string {
	for _, phase := range b.Phases {
		for _, id := range phase.Plans {
			if !r.IsTerminal(id) {
				return id
			}
		}
	}
	return ""
}
