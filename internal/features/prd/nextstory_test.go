package prd_test

import (
	"testing"

	"springfield/internal/features/prd"
)

func TestNextEligibleStoryPicksHighestPriority(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-002", Priority: 2, Passes: false},
		{ID: "US-001", Priority: 1, Passes: false},
	}}
	s, ok := prd.NextEligibleStory(p)
	if !ok {
		t.Fatal("expected an eligible story")
	}
	if s.ID != "US-001" {
		t.Fatalf("expected US-001 (lower priority number wins), got %s", s.ID)
	}
}

func TestNextEligibleStoryTiebreaksLexicographically(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-003", Priority: 1, Passes: false},
		{ID: "US-001", Priority: 1, Passes: false},
		{ID: "US-002", Priority: 1, Passes: false},
	}}
	s, ok := prd.NextEligibleStory(p)
	if !ok || s.ID != "US-001" {
		t.Fatalf("expected US-001 (lex smallest at same priority), got %q ok=%v", s.ID, ok)
	}
}

func TestNextEligibleStorySkipsUnsatisfiedDeps(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Priority: 1, Passes: false, Deps: []string{"US-002"}},
		{ID: "US-002", Priority: 2, Passes: false},
	}}
	s, ok := prd.NextEligibleStory(p)
	if !ok || s.ID != "US-002" {
		t.Fatalf("expected US-002 (US-001 dep unmet), got %q ok=%v", s.ID, ok)
	}
}

func TestNextEligibleStoryHonorsSatisfiedDeps(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Priority: 1, Passes: false, Deps: []string{"US-002"}},
		{ID: "US-002", Priority: 2, Passes: true},
	}}
	s, ok := prd.NextEligibleStory(p)
	if !ok || s.ID != "US-001" {
		t.Fatalf("expected US-001 (dep satisfied), got %q ok=%v", s.ID, ok)
	}
}

func TestNextEligibleStoryFalseWhenAllPassed(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Passes: true},
		{ID: "US-002", Passes: true},
	}}
	if _, ok := prd.NextEligibleStory(p); ok {
		t.Fatal("expected no eligible story when all passed")
	}
}

func TestNextEligibleStoryFalseWhenEmpty(t *testing.T) {
	if _, ok := prd.NextEligibleStory(prd.PRD{}); ok {
		t.Fatal("expected no eligible story for an empty plan")
	}
}

func TestNextEligibleStoryFalseWhenBlockedByCycle(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Priority: 1, Passes: false, Deps: []string{"US-002"}},
		{ID: "US-002", Priority: 2, Passes: false, Deps: []string{"US-001"}},
	}}
	if _, ok := prd.NextEligibleStory(p); ok {
		t.Fatal("expected no eligible story for a dependency cycle")
	}
}
