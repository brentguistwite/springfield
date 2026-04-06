package ralph_test

import (
	"testing"

	"springfield/internal/features/ralph"
)

func TestWorkspaceListPlansReturnsStableOrder(t *testing.T) {
	rootDir := t.TempDir()
	workspace, err := ralph.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("open root workspace: %v", err)
	}

	if err := workspace.InitPlan("zeta", ralph.Spec{Project: "springfield"}); err != nil {
		t.Fatalf("init zeta plan: %v", err)
	}
	if err := workspace.InitPlan("alpha", ralph.Spec{Project: "springfield"}); err != nil {
		t.Fatalf("init alpha plan: %v", err)
	}

	plans, err := workspace.ListPlans()
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}

	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].Name != "alpha" || plans[1].Name != "zeta" {
		t.Fatalf("expected stable sorted order, got %+v", plans)
	}
}
