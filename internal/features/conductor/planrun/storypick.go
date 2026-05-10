package planrun

import (
	"sort"

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
// Eligibility: passes == false AND every dep has passes == true (within the
// same plan). Among eligible, the highest-priority story (lower priority
// number = higher priority) wins. Tiebreak: lexicographically smallest ID.
func NextStory(plan prd.PRD) (prd.UserStory, PickStatus) {
	// Build a set of passed story IDs for dep checking.
	passed := make(map[string]bool, len(plan.UserStories))
	for _, s := range plan.UserStories {
		if s.Passes {
			passed[s.ID] = true
		}
	}

	var eligible []prd.UserStory
	anyPending := false
	for _, s := range plan.UserStories {
		if s.Passes {
			continue
		}
		anyPending = true
		depsOK := true
		for _, dep := range s.Deps {
			if !passed[dep] {
				depsOK = false
				break
			}
		}
		if depsOK {
			eligible = append(eligible, s)
		}
	}

	if !anyPending {
		// No unpassed stories — zero-story or all done.
		return prd.UserStory{}, PickAllPassed
	}

	if len(eligible) == 0 {
		// Stories remain but none are eligible — blocked (cycle or unresolvable deps).
		return prd.UserStory{}, PickBlocked
	}

	sort.Slice(eligible, func(i, j int) bool {
		pi, pj := eligible[i].Priority, eligible[j].Priority
		if pi != pj {
			return pi < pj
		}
		return eligible[i].ID < eligible[j].ID
	})

	return eligible[0], PickReady
}
