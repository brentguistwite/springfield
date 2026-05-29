package conductor_test

import (
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// TestMarkPlanCompletedRejectsUnpassedStories verifies the validation gate:
// MarkPlanCompleted refuses when any story is not passing and names the
// offenders, leaving the plan's state untouched.
func TestMarkPlanCompletedRejectsUnpassedStories(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")

	stories := []prd.UserStory{
		{ID: "US-001", Passes: true},
		{ID: "US-002", Passes: false},
		{ID: "US-003", Passes: false},
	}
	_, err = project.MarkPlanCompleted("alpha", stories)
	if err == nil {
		t.Fatal("expected rejection for unpassed stories, got nil")
	}
	if !strings.Contains(err.Error(), "US-002") || !strings.Contains(err.Error(), "US-003") {
		t.Errorf("error should name unpassed stories US-002 and US-003: %v", err)
	}
	if strings.Contains(err.Error(), "US-001") {
		t.Errorf("error should not name the passing story US-001: %v", err)
	}

	// State must be untouched on rejection.
	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusFailed {
		t.Fatalf("status mutated on rejection: %q", ps.Status)
	}
	if ps.Merge != nil {
		t.Fatal("merge should not be set on rejection")
	}
}

// TestMarkPlanCompletedAcceptsAllPassing verifies the success path: status
// flips to completed, Merge becomes pending with a timestamp, Error clears,
// and a recovery-history entry is recorded.
func TestMarkPlanCompletedAcceptsAllPassing(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")

	stories := []prd.UserStory{
		{ID: "US-001", Passes: true},
		{ID: "US-002", Passes: true},
	}
	rec, err := project.MarkPlanCompleted("alpha", stories)
	if err != nil {
		t.Fatalf("MarkPlanCompleted: %v", err)
	}
	if rec.Action != "mark-completed" {
		t.Errorf("action = %q, want mark-completed", rec.Action)
	}

	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusCompleted {
		t.Fatalf("status = %q, want completed", ps.Status)
	}
	if ps.Error != "" {
		t.Errorf("error should be cleared, got %q", ps.Error)
	}
	if ps.Merge == nil || ps.Merge.Status != conductor.MergePending {
		t.Fatalf("merge = %+v, want pending", ps.Merge)
	}
	if ps.Merge.AttemptedAt.IsZero() {
		t.Error("merge.attempted_at should be set")
	}
	if len(ps.RecoveryHistory) != 1 || ps.RecoveryHistory[0].Action != "mark-completed" {
		t.Fatalf("recovery history = %+v", ps.RecoveryHistory)
	}
}

// TestMarkPlanCompletedRejectsAlreadyCompleted verifies an already-completed
// plan is rejected (use retry-merge/retry-integration instead).
func TestMarkPlanCompletedRejectsAlreadyCompleted(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkCompleted("alpha", "claude", "")

	_, err = project.MarkPlanCompleted("alpha", []prd.UserStory{{ID: "US-001", Passes: true}})
	if err == nil {
		t.Fatal("expected rejection for already-completed plan")
	}
	if !strings.Contains(err.Error(), "already completed") {
		t.Errorf("error should explain already-completed: %v", err)
	}
}

// TestMarkPlanCompletedRejectsNoStories guards against marking a plan completed
// when its prd.json has no stories — an empty story set is not "all passing".
func TestMarkPlanCompletedRejectsNoStories(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")

	_, err = project.MarkPlanCompleted("alpha", nil)
	if err == nil {
		t.Fatal("expected rejection for plan with no stories")
	}
}
