package planrun

import (
	"testing"

	"springfield/internal/features/prd"
)

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
