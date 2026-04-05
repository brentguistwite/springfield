package ralph_test

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/ralph"
)

func TestPlanNextEligibleHonorsDependencies(t *testing.T) {
	plan := ralph.Plan{
		Name: "demo",
		Spec: ralph.Spec{
			Project: "springfield",
			Stories: []ralph.Story{
				{ID: "US-001", Title: "Bootstrap", Priority: 1, Passed: true},
				{ID: "US-002", Title: "Refresh prompt", Priority: 2, DependsOn: []string{"US-001"}},
				{ID: "US-003", Title: "Blocked follow-up", Priority: 3, DependsOn: []string{"US-999"}},
			},
		},
	}

	story, ok := plan.NextEligible()
	if !ok {
		t.Fatal("expected an eligible story")
	}

	if story.ID != "US-002" {
		t.Fatalf("expected US-002, got %s", story.ID)
	}
}

func TestPlanFindStory(t *testing.T) {
	plan := ralph.Plan{
		Spec: ralph.Spec{
			Stories: []ralph.Story{
				{ID: "US-001", Title: "Bootstrap"},
				{ID: "US-002", Title: "Refresh"},
			},
		},
	}

	story, ok := plan.FindStory("US-002")
	if !ok {
		t.Fatal("expected story to be found")
	}

	if story.Title != "Refresh" {
		t.Fatalf("expected title Refresh, got %q", story.Title)
	}

	if _, ok := plan.FindStory("US-404"); ok {
		t.Fatal("expected missing story lookup to fail")
	}
}

func TestWorkspaceInitPlanAndLoadPlan(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open root workspace: %v", err)
	}

	spec := ralph.Spec{
		Project:     "springfield",
		Description: "refresh Ralph",
		Stories: []ralph.Story{
			{ID: "US-001", Title: "Bootstrap"},
		},
	}

	if err := workspace.InitPlan("refresh", spec); err != nil {
		t.Fatalf("init plan: %v", err)
	}

	planPath := filepath.Join(rootDir, ".springfield", "ralph", "plans", "refresh.json")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("expected persisted plan at %s: %v", planPath, err)
	}

	plan, err := workspace.LoadPlan("refresh")
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	if plan.Name != "refresh" {
		t.Fatalf("expected plan name refresh, got %q", plan.Name)
	}

	if plan.Spec.Project != "springfield" {
		t.Fatalf("expected project springfield, got %q", plan.Spec.Project)
	}
}
