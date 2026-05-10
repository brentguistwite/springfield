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
	story, status := planrun.NextStory(plan)
	if status != planrun.PickReady {
		t.Fatalf("expected PickReady, got %v", status)
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
	story, status := planrun.NextStory(plan)
	if status != planrun.PickReady {
		t.Fatalf("expected PickReady, got %v", status)
	}
	// US-001 has unmet dep on US-002; US-002 is eligible (lower priority num = higher pri but US-001 blocked)
	if story.ID != "US-002" {
		t.Fatalf("expected US-002 (dep is blocking US-001), got %s", story.ID)
	}
}

func TestNextStoryReturnsAllPassedWhenAllPassed(t *testing.T) {
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Passes: true},
			{ID: "US-002", Passes: true},
		},
	}
	_, status := planrun.NextStory(plan)
	if status != planrun.PickAllPassed {
		t.Fatalf("expected PickAllPassed when all stories passed, got %v", status)
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
	story, status := planrun.NextStory(plan)
	if status != planrun.PickReady {
		t.Fatalf("expected PickReady, got %v", status)
	}
	if story.ID != "US-002" {
		t.Fatalf("expected US-002 (US-001 blocked, US-003 lower priority), got %s", story.ID)
	}
}

func TestNextStoryReturnsAllPassedForEmptyPlan(t *testing.T) {
	plan := prd.PRD{}
	_, status := planrun.NextStory(plan)
	if status != planrun.PickAllPassed {
		t.Fatalf("expected PickAllPassed for empty plan, got %v", status)
	}
}

func TestNextStoryHonorsDepsSatisfied(t *testing.T) {
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Priority: 1, Passes: false, Deps: []string{"US-002"}},
			{ID: "US-002", Priority: 2, Passes: true},
		},
	}
	story, status := planrun.NextStory(plan)
	if status != planrun.PickReady {
		t.Fatalf("expected PickReady (dep satisfied), got %v", status)
	}
	if story.ID != "US-001" {
		t.Fatalf("expected US-001 (dep satisfied), got %s", story.ID)
	}
}

func TestNextStoryReturnsBlockedForCyclicDeps(t *testing.T) {
	// US-001 deps US-002, US-002 deps US-001 → cycle; US-003 is already passed.
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Priority: 1, Passes: false, Deps: []string{"US-002"}},
			{ID: "US-002", Priority: 2, Passes: false, Deps: []string{"US-001"}},
			{ID: "US-003", Priority: 3, Passes: true},
		},
	}
	_, status := planrun.NextStory(plan)
	if status != planrun.PickBlocked {
		t.Fatalf("expected PickBlocked for cyclic deps, got %v", status)
	}
}

func TestNextStoryReturnsAllPassedWhenAllDone(t *testing.T) {
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Priority: 1, Passes: true},
			{ID: "US-002", Priority: 2, Passes: true},
			{ID: "US-003", Priority: 3, Passes: true},
		},
	}
	_, status := planrun.NextStory(plan)
	if status != planrun.PickAllPassed {
		t.Fatalf("expected PickAllPassed, got %v", status)
	}
}

func TestNextStoryReturnsReadyForFirstEligible(t *testing.T) {
	plan := prd.PRD{
		UserStories: []prd.UserStory{
			{ID: "US-001", Priority: 1, Passes: false},
			{ID: "US-002", Priority: 2, Passes: false},
		},
	}
	story, status := planrun.NextStory(plan)
	if status != planrun.PickReady {
		t.Fatalf("expected PickReady, got %v", status)
	}
	if story.ID != "US-001" {
		t.Fatalf("expected US-001, got %s", story.ID)
	}
}
