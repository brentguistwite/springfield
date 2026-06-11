package planrun

import (
	"testing"

	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/prd"
)

// TestIterationStoryComplete pins the mid-plan turn-cap defuse (dogfood flo360
// #3): an over-cap iteration is honored ONLY when it emitted its TARGET story's
// pass marker AND the worktree advanced (proof of commit). Off-target markers,
// a missing marker, an unchanged/empty head each leave it un-honored so the cap
// still fires.
func TestIterationStoryComplete(t *testing.T) {
	pass := func(id string) []coreexec.Event {
		return []coreexec.Event{{Type: coreexec.EventStdout, Data: "<story-pass>" + id + "</story-pass>"}}
	}
	none := []coreexec.Event{{Type: coreexec.EventStdout, Data: "burned turns, no marker"}}

	cases := []struct {
		name         string
		events       []coreexec.Event
		target       string
		baseHead     string
		worktreeHead string
		want         bool
	}{
		{"target pass + committed", pass("US-001"), "US-001", "base", "moved", true},
		{"target pass but no commit (equal heads)", pass("US-001"), "US-001", "same", "same", false},
		{"off-target pass + committed", pass("US-002"), "US-001", "base", "moved", false},
		{"no marker + committed", none, "US-001", "base", "moved", false},
		{"target pass + empty base head", pass("US-001"), "US-001", "", "moved", false},
		{"target pass + empty worktree head", pass("US-001"), "US-001", "base", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := iterationStoryComplete(tc.events, tc.target, tc.baseHead, tc.worktreeHead)
			if got != tc.want {
				t.Fatalf("iterationStoryComplete(%q, %q, %q) = %v, want %v",
					tc.target, tc.baseHead, tc.worktreeHead, got, tc.want)
			}
		})
	}
}

// TestWorkCompletedBeforeCrash pins the three-condition gate that lets a plan
// be judged complete despite a non-zero process exit. ALL of (a) COMPLETE
// honored, (b) every story passed, (c) worktree advanced beyond base must hold;
// any one failing returns false so the normal exit-code logic applies.
func TestWorkCompletedBeforeCrash(t *testing.T) {
	allPassed := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Passes: true},
		{ID: "US-002", Passes: true},
	}}
	onePending := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Passes: true},
		{ID: "US-002", Passes: false},
	}}
	empty := prd.PRD{UserStories: nil}

	cases := []struct {
		name            string
		completeHonored bool
		p               prd.PRD
		baseHead        string
		worktreeHead    string
		want            bool
	}{
		{"all three hold", true, allPassed, "base", "moved", true},
		{"complete not honored", false, allPassed, "base", "moved", false},
		{"a story still pending", true, onePending, "base", "moved", false},
		{"worktree did not advance", true, allPassed, "same", "same", false},
		{"empty base head", true, allPassed, "", "moved", false},
		{"empty worktree head", true, allPassed, "base", "", false},
		{"zero-story prd never completes", true, empty, "base", "moved", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workCompletedBeforeCrash(tc.completeHonored, tc.p, tc.baseHead, tc.worktreeHead)
			if got != tc.want {
				t.Fatalf("workCompletedBeforeCrash(%v, …, %q, %q) = %v, want %v",
					tc.completeHonored, tc.baseHead, tc.worktreeHead, got, tc.want)
			}
		})
	}
}
