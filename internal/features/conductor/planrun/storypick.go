package planrun

import (
	"springfield/internal/features/prd"
)

// PickStatus describes the outcome of a NextStory call.
type PickStatus int

const (
	// PickReady means a story was found and is ready to work on.
	PickReady PickStatus = iota
	// PickAllPassed means every story in the plan has passes=true (or the plan
	// has no stories). The caller should treat this as successful completion.
	PickAllPassed
	// PickBlocked means some stories are not yet passed but none have all
	// dependencies satisfied — typically a cycle or other unsatisfiable
	// dependency graph. The caller must fail the plan rather than loop forever.
	PickBlocked
)

// NextStory returns the next eligible user story to work on and a PickStatus.
//
//   - PickReady: story returned, work to do.
//   - PickAllPassed: every story has passes=true (or plan is empty). Plan complete.
//   - PickBlocked: some stories not passed but none have all deps satisfied
//     (cycle / unsatisfiable graph). Caller must fail the plan.
//
// Eligibility (passes == false AND every dep passed; highest priority wins,
// lexicographic ID tiebreak) is single-sourced in prd.NextEligibleStory so the
// runner and the status projection cannot disagree about which story is current.
// NextStory adds only the PickStatus taxonomy on top: it disambiguates the
// no-eligible-story case into PickAllPassed (nothing left to do) vs PickBlocked
// (work remains but an unsatisfiable graph stalls it).
func NextStory(plan prd.PRD) (prd.UserStory, PickStatus) {
	anyPending := false
	for _, s := range plan.UserStories {
		if !s.Passes {
			anyPending = true
			break
		}
	}
	if !anyPending {
		// No unpassed stories — zero-story or all done.
		return prd.UserStory{}, PickAllPassed
	}

	story, ok := prd.NextEligibleStory(plan)
	if !ok {
		// Stories remain but none are eligible — blocked (cycle or unresolvable deps).
		return prd.UserStory{}, PickBlocked
	}
	return story, PickReady
}
