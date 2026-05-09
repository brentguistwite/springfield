package conductor_test

import (
	"reflect"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// TestCompile_ThreePlansYieldThreeUnitsWithCorrectPathAndOrder verifies that
// batch.Compile produces one PlanUnit per envelope plan with:
//   - Path ending in /prd.json
//   - Order matching first-phase-appearance index.
func minStory() prd.UserStory {
	return prd.UserStory{
		ID:                 "US-001",
		Title:              "placeholder",
		Priority:           1,
		AcceptanceCriteria: []string{"passes"},
	}
}

func minPlan(id, title string) prd.BatchPRDPlan {
	return prd.BatchPRDPlan{PRD: prd.PRD{ID: id, Title: title, UserStories: []prd.UserStory{minStory()}}}
}

func TestCompile_ThreePlansYieldThreeUnitsWithCorrectPathAndOrder(t *testing.T) {
	env := prd.BatchPRDEnvelope{
		Title:  "three plans",
		Source: "test",
		Phases: []prd.PhasePRD{
			{Mode: "serial", Plans: []string{"alpha", "beta"}},
			{Mode: "serial", Plans: []string{"beta", "gamma"}}, // beta already seen
		},
		Plans: []prd.BatchPRDPlan{
			minPlan("alpha", "Alpha"),
			minPlan("beta", "Beta"),
			minPlan("gamma", "Gamma"),
		},
	}

	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	if len(out.Units) != 3 {
		t.Fatalf("Units count = %d, want 3", len(out.Units))
	}

	byID := map[string]struct {
		path  string
		order int
	}{}
	for _, u := range out.Units {
		byID[u.ID] = struct {
			path  string
			order int
		}{u.Path, u.Order}
	}

	// All paths must end in /prd.json
	for id, info := range byID {
		if !strings.HasSuffix(info.path, "/prd.json") {
			t.Errorf("plan %q Path = %q, want suffix /prd.json", id, info.path)
		}
	}

	// Order: alpha=1, beta=2, gamma=3 (first-seen across phases)
	want := map[string]int{"alpha": 1, "beta": 2, "gamma": 3}
	for id, wantOrder := range want {
		if info, ok := byID[id]; !ok {
			t.Errorf("missing PlanUnit for %q", id)
		} else if info.order != wantOrder {
			t.Errorf("plan %q Order = %d, want %d", id, info.order, wantOrder)
		}
	}

	// Path canonical form: .springfield/plans/<id>/prd.json
	for id, info := range byID {
		wantPath := ".springfield/plans/" + id + "/prd.json"
		if info.path != wantPath {
			t.Errorf("plan %q Path = %q, want %q", id, info.path, wantPath)
		}
	}
}

// TestPlanUnitDescriptionFieldDropped verifies that the Description field was
// removed from PlanUnit (Phase 2 spec: drop Description field entirely).
func TestPlanUnitDescriptionFieldDropped(t *testing.T) {
	_ = batch.CompileInput{} // keep batch import used
	typ := reflect.TypeOf(conductor.PlanUnit{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Name == "Description" {
			t.Errorf("PlanUnit.Description field re-introduced; was dropped per Phase 2 spec")
		}
	}
}
