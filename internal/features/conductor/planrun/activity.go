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
