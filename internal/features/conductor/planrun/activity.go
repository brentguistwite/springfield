package planrun

import (
	"time"

	"springfield/internal/features/conductor"
)

// enterPhase is the SINGLE funnel a running plan stamps its in-flight Activity
// signal through. It writes PlanActivity{phase, detail, round} onto the plan's
// PlanState and immediately persists via SaveState so a concurrent status
// reader observes the transition mid-run. Every phase-execution site (the story
// loop, and — retrofitted by later stories — the review and verify gates) MUST
// route its transition through here; that single-writer discipline is what the
// coverage-enforcement AST scan asserts, so no other code path may mutate
// PlanState.Activity directly.
//
// It degrades to silence, never to a lie:
//   - A plan with no persisted running state (nil PlanState) is a no-op — there
//     is nothing truthful to stamp onto.
//   - A SaveState failure is returned, NOT swallowed, but callers treat it as a
//     best-effort progress stamp: the durable coarse phase is derived from
//     prd.json regardless, so a missed write leaves the reader on the derived
//     truth rather than a stale fabrication. Failing the whole plan over a
//     progress-signal write would be a worse outcome than a momentarily-absent
//     round counter.
func enterPhase(p *conductor.Project, planID, phase, detail string, round int, now func() time.Time) error {
	if p == nil {
		return nil
	}
	ps := p.State.Plans[planID]
	if ps == nil {
		return nil
	}
	// An empty phase is the CLEAR signal (see clearActivity): drop the in-flight
	// fine signal so a reader falls back to the derived coarse phase (Tier 1)
	// rather than a stranded phase from a gate that has already returned. Nothing
	// stamped means nothing to clear — stay silent and skip the write.
	if phase == "" {
		if ps.Activity == nil {
			return nil
		}
		ps.Activity = nil
		return p.SaveState()
	}
	stamp := time.Now
	if now != nil {
		stamp = now
	}
	ps.Activity = &conductor.PlanActivity{
		Phase:     phase,
		Detail:    detail,
		Round:     round,
		UpdatedAt: stamp(),
	}
	return p.SaveState()
}

// clearActivity drops a running plan's in-flight Activity so a status reader
// falls back to the derived coarse phase instead of a fine phase stranded by a
// gate that has already returned. It routes through enterPhase (the sole
// PlanActivity writer, per the coverage-enforcement AST scan) with an empty
// phase, so a gate can `defer clearActivity(...)` to guarantee its round is
// cleared on EVERY exit — normal return, error, or panic unwinding. The write
// error is intentionally dropped: a failed clear leaves the reader on a stale
// fine phase for at most one status read, and there is no caller better placed
// to react to it than the derived-truth fallback the projection already applies.
func clearActivity(p *conductor.Project, planID string, now func() time.Time) {
	_ = enterPhase(p, planID, "", "", 0, now)
}
