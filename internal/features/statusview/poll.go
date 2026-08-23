package statusview

import (
	"path/filepath"

	"springfield/internal/core/lock"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
	"springfield/internal/features/prd"
)

// Poll reads the Springfield control plane rooted at root READ-ONLY and returns
// the current status View — the exact value `springfield status --json` emits.
// It is the single projection shared by the --json branch and the --watch loop
// so the two surfaces can never render from a second, drifting projection.
//
// Read-only contract: Poll only reads run.json, the batch record, the conductor
// state, per-plan prd.json, the evidence cost files, and inspects the
// control-plane lock. It never writes, creates, or locks anything under
// .springfield/ — a watcher polling on an interval must leave the directory
// byte-identical (the runner is the sole writer). Unlike the text registry
// path, Poll never rewrites a stale running plan to interrupted.
//
// State branches mirror cmd/status.go's --json arbitration exactly: no run (or a
// cleared cursor) yields the latest Archived batch when one exists else Idle; a
// run whose batch.json is missing yields Orphan; otherwise Active. A failure to
// load the conductor project degrades to a state-less Active (progress/spend
// suppressed) rather than an error, matching the text path.
func Poll(root string) (View, error) {
	run, hasRun, err := batch.ReadRun(root)
	if err != nil {
		return View{}, err
	}
	if !hasRun || run.ActiveBatchID == "" {
		// Once the run cursor is cleared, the just-completed batch's per-plan
		// results live only in the archive. Surface the latest archive so a
		// watcher sees the final frame; fall to idle only when nothing was ever
		// archived.
		if entry, ok, archErr := batch.LatestArchive(root); archErr == nil && ok {
			return Archived(entry, root), nil
		}
		return Idle(), nil
	}

	paths, err := batch.NewPaths(root, run.ActiveBatchID)
	if err != nil {
		return View{}, err
	}
	b, err := batch.ReadBatch(paths)
	if err != nil {
		if batch.IsMissingBatchError(err) {
			return Orphan(run), nil
		}
		return View{}, err
	}

	var state *conductor.State
	var units []conductor.PlanUnit
	if project, loadErr := conductor.LoadProjectRaw(root); loadErr == nil {
		state = project.State
		units = project.Config.PlanUnits
	}

	// Read-only liveness probe: a confirmed holder (PID != 0) means a live
	// springfield process owns the control-plane lock, so in-flight plans are
	// genuinely running; otherwise a started-but-non-terminal plan is stalled.
	held := lock.Inspect(root)
	live := held != nil && held.PID != 0

	rollup, rollupErr := cost.ComputeRollup(root, b.ID)
	effectiveFatalError := ""
	if run.FatalError != "" && BatchHasFailedPlan(b, state) {
		effectiveFatalError = run.FatalError
	}
	in := ActiveInput{
		Batch:      b,
		Run:        run,
		State:      state,
		Units:      units,
		Rollup:     rollup,
		HasRollup:  rollupErr == nil && rollup.Iterations > 0,
		FatalError: effectiveFatalError,
		Live:       live,
		PRDs:       LoadPlanPRDs(root, units),
	}
	return Active(in), nil
}

// LoadPlanPRDs reads each plan's persisted prd.json so the projection can derive
// the in-flight coarse phase (the current story) from durable truth. It is
// best-effort by design: a legacy .md plan-unit path or a missing/malformed
// prd.json is simply skipped, so the plan gets no derived current story rather
// than a fabricated one — the derivation degrades to silence, never a lie.
func LoadPlanPRDs(root string, units []conductor.PlanUnit) map[string]prd.PRD {
	out := make(map[string]prd.PRD, len(units))
	for _, u := range units {
		if filepath.Base(u.Path) != "prd.json" {
			continue
		}
		p, err := prd.ParseFile(filepath.Join(root, u.Path))
		if err != nil {
			continue
		}
		out[u.ID] = p
	}
	return out
}

// BatchHasFailedPlan reports whether any plan in the batch is still in a halting
// state (failed or needs-human) per the conductor snapshot. It gates whether the
// batch-level fatal error is still operator-actionable: once the halting plan is
// recovered, the now-stale error is suppressed. When state is nil the snapshot
// is unavailable, so the error cannot be proven stale and is kept.
func BatchHasFailedPlan(b batch.Batch, state *conductor.State) bool {
	if state == nil {
		return true
	}
	for _, id := range b.PlanIDs {
		ps, ok := state.Plans[id]
		if !ok || ps == nil {
			continue
		}
		if ps.Status == conductor.StatusFailed || ps.Status == conductor.StatusNeedsHuman {
			return true
		}
	}
	return false
}
