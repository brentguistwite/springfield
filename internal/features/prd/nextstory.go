package prd

import "sort"

// NextEligibleStory returns the highest-priority story that is ready to work on
// and whether one exists. A story is eligible when passes == false AND every one
// of its deps has passes == true (within the same plan). Among eligible stories,
// the lower priority number wins (higher priority); ties break lexicographically
// by ID.
//
// This is the SINGLE source of the story-eligibility rule. Both the runner's
// story picker (planrun.NextStory) and the status projection's derived current
// story (statusview) delegate here, so the "what story is current" answer cannot
// drift between what the runner works on and what status reports — a drift would
// make the in-flight Activity phase lie about the plan's real position.
//
// It does NOT distinguish "all passed" from "blocked by an unsatisfiable graph":
// both yield ok == false. Callers that need that distinction (the runner, to
// decide complete-vs-fail) inspect the passes field themselves.
func NextEligibleStory(p PRD) (UserStory, bool) {
	passed := make(map[string]bool, len(p.UserStories))
	for _, s := range p.UserStories {
		if s.Passes {
			passed[s.ID] = true
		}
	}

	var eligible []UserStory
	for _, s := range p.UserStories {
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
		return UserStory{}, false
	}

	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].Priority != eligible[j].Priority {
			return eligible[i].Priority < eligible[j].Priority
		}
		return eligible[i].ID < eligible[j].ID
	})
	return eligible[0], true
}
