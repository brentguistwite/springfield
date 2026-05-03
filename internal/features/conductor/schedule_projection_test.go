package conductor_test

import (
	"reflect"
	"testing"

	"springfield/internal/features/conductor"
)

func TestBuildScheduleHonorsPlanUnitsOverLegacy(t *testing.T) {
	cfg := &conductor.Config{
		Sequential: []string{"legacy-a"},
		Batches:    [][]string{{"legacy-b"}},
		PlanUnits: []conductor.PlanUnit{
			{ID: "feature-b", Path: "springfield/plans/b.md", Order: 2},
			{ID: "feature-a", Path: "springfield/plans/a.md", Order: 1},
		},
	}
	schedule := conductor.BuildSchedule(cfg)
	state := conductor.NewState()
	got := schedule.NextPlans(state)
	if len(got) != 1 || got[0] != "feature-a" {
		t.Fatalf("got %v, want [feature-a]", got)
	}
}

func TestBuildScheduleFallsBackToLegacy(t *testing.T) {
	cfg := &conductor.Config{
		Sequential: []string{"legacy-a", "legacy-b"},
		Batches:    [][]string{{"legacy-c", "legacy-d"}},
	}
	schedule := conductor.BuildSchedule(cfg)
	state := conductor.NewState()
	got := schedule.NextPlans(state)
	if len(got) != 1 || got[0] != "legacy-a" {
		t.Fatalf("got %v, want [legacy-a]", got)
	}
}

func TestProjectAllPlansFromPlanUnits(t *testing.T) {
	cfg := &conductor.Config{
		PlanUnits: []conductor.PlanUnit{
			{ID: "z", Path: "springfield/plans/z.md", Order: 3},
			{ID: "a", Path: "springfield/plans/a.md", Order: 1},
			{ID: "m", Path: "springfield/plans/m.md", Order: 2},
		},
	}
	project := &conductor.Project{Config: cfg, State: conductor.NewState()}
	want := []string{"a", "m", "z"}
	if got := project.AllPlans(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
