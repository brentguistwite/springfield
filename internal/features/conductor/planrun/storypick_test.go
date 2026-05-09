package planrun_test

import (
	"testing"

	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/prd"
)

func TestNextStoryReturnsSmallestIDWhenAllEligibleSamePriority(t *testing.T) {
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-003", Priority: 1, Passes: false},
			{ID: "US-001", Priority: 1, Passes: false},
			{ID: "US-002", Priority: 1, Passes: false},
		},
	}
	story, ok := planrun.NextStory(plan)
	if !ok {
		t.Fatal("expected a story to be returned")
	}
	if story.ID != "US-001" {
		t.Fatalf("expected US-001 (lex smallest), got %s", story.ID)
	}
}

func TestNextStorySkipsUnsatisfiedDeps(t *testing.T) {
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Priority: 1, Passes: false, Deps: []string{"US-002"}},
			{ID: "US-002", Priority: 2, Passes: false},
		},
	}
	story, ok := planrun.NextStory(plan)
	if !ok {
		t.Fatal("expected a story")
	}
	// US-001 has unmet dep on US-002; US-002 is eligible (lower priority num = higher pri but US-001 blocked)
	if story.ID != "US-002" {
		t.Fatalf("expected US-002 (dep is blocking US-001), got %s", story.ID)
	}
}

func TestNextStoryReturnsFalseWhenAllPassed(t *testing.T) {
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Passes: true},
			{ID: "US-002", Passes: true},
		},
	}
	_, ok := planrun.NextStory(plan)
	if ok {
		t.Fatal("expected false when all stories passed")
	}
}

func TestNextStoryPicksLowerPriorityWhenHigherIsBlocked(t *testing.T) {
	// US-001 has priority 1 (highest) but dep US-003 not passed
	// US-002 has priority 2, no deps → should be picked
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Priority: 1, Passes: false, Deps: []string{"US-003"}},
			{ID: "US-002", Priority: 2, Passes: false},
			{ID: "US-003", Priority: 3, Passes: false},
		},
	}
	story, ok := planrun.NextStory(plan)
	if !ok {
		t.Fatal("expected a story")
	}
	if story.ID != "US-002" {
		t.Fatalf("expected US-002 (US-001 blocked, US-003 lower priority), got %s", story.ID)
	}
}

func TestNextStoryReturnsFalseForEmptyPlan(t *testing.T) {
	plan := prd.PRD{}
	_, ok := planrun.NextStory(plan)
	if ok {
		t.Fatal("expected false for empty plan")
	}
}

func TestNextStoryHonorsDepsSatisfied(t *testing.T) {
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Priority: 1, Passes: false, Deps: []string{"US-002"}},
			{ID: "US-002", Priority: 2, Passes: true},
		},
	}
	story, ok := planrun.NextStory(plan)
	if !ok {
		t.Fatal("expected a story (dep satisfied)")
	}
	if story.ID != "US-001" {
		t.Fatalf("expected US-001 (dep satisfied), got %s", story.ID)
	}
}
