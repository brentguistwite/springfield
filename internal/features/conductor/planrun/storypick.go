package planrun

import (
	"sort"

	"springfield/internal/features/prd"
)

// NextStory returns the next eligible user story to work on, or false when
// all stories in the plan are passed.
//
// Eligibility: passes == false AND every dep has passes == true (within the
// same plan). Among eligible, the highest-priority story (lower priority
// number = higher priority) wins. Tiebreak: lexicographically smallest ID.
func NextStory(plan prd.PRD) (prd.UserStory, bool) {
	// Build a set of passed story IDs for dep checking.
	passed := make(map[string]bool, len(plan.UserStories))
	for _, s := range plan.UserStories {
		if s.Passes {
			passed[s.ID] = true
		}
	}

	var eligible []prd.UserStory
	for _, s := range plan.UserStories {
		if s.Passes {
			continue
		}
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

	if len(eligible) == 0 {
		return prd.UserStory{}, false
	}

	sort.Slice(eligible, func(i, j int) bool {
		pi, pj := eligible[i].Priority, eligible[j].Priority
		if pi != pj {
			return pi < pj
		}
		return eligible[i].ID < eligible[j].ID
	})

	return eligible[0], true
}
