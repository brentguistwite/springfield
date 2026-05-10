package batch_test

import (
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/prd"
)

// minStory returns a minimal valid UserStory.
func minStory(id string) prd.UserStory {
	return prd.UserStory{
		ID:                 id,
		Title:              "Story " + id,
		Priority:           1,
		AcceptanceCriteria: []string{"passes"},
	}
}

// minPlan returns a minimal valid BatchPRDPlan.
func minPlan(id, title string) prd.BatchPRDPlan {
	return prd.BatchPRDPlan{
		PRD: prd.PRD{
			ID:          id,
			Title:       title,
			UserStories: []prd.UserStory{minStory("US-001")},
		},
	}
}

// TestCompileBatchIDDoesNotCollideWithPlanID verifies that when the envelope
// title sanitizes to the same slug as a plan ID, the batch gets a distinct ID.
func TestCompileBatchIDDoesNotCollideWithPlanID(t *testing.T) {
	env := prd.BatchPRDEnvelope{
		Title:  "alpha", // sanitizes to "alpha" — same as plan ID
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"alpha"}}},
		Plans:  []prd.BatchPRDPlan{minPlan("alpha", "Alpha Plan")},
	}

	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID == "alpha" {
		t.Errorf("batch ID = %q collides with plan ID %q; want a distinct ID (e.g. alpha-2)", out.Batch.ID, "alpha")
	}
}

// TestCompileBatchIDNoCollisionWhenTitleDiffers verifies that when the title
// does NOT match any plan ID, the batch ID is the sanitized title directly.
func TestCompileBatchIDNoCollisionWhenTitleDiffers(t *testing.T) {
	env := prd.BatchPRDEnvelope{
		Title:  "my-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"alpha"}}},
		Plans:  []prd.BatchPRDPlan{minPlan("alpha", "Alpha Plan")},
	}

	out, err := batch.Compile(batch.CompileInput{Envelope: env})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID != "my-batch" {
		t.Errorf("batch ID = %q, want %q", out.Batch.ID, "my-batch")
	}
}

// TestCompileBatchIDDoesNotCollideWithRegisteredPlanID verifies that when the
// envelope title sanitizes to the same slug as a standalone registered plan ID,
// the batch ID is made distinct. This prevents filesystem collisions between
// batch directory and plan directory under .springfield/plans/<id>/.
func TestCompileBatchIDDoesNotCollideWithRegisteredPlanID(t *testing.T) {
	env := prd.BatchPRDEnvelope{
		Title:  "alpha", // sanitizes to "alpha" — same as registered standalone plan
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"beta"}}},
		Plans:  []prd.BatchPRDPlan{minPlan("beta", "Beta Plan")},
	}

	out, err := batch.Compile(batch.CompileInput{
		Envelope:          env,
		RegisteredPlanIDs: map[string]struct{}{"alpha": {}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID == "alpha" {
		t.Errorf("batch ID = %q collides with registered plan ID %q; want a distinct ID", out.Batch.ID, "alpha")
	}
}
