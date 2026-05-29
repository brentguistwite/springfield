package conductor

import "testing"

func TestAvailableActionsNeedsHumanOffersRetry(t *testing.T) {
	actions := availableActions(&PlanState{Status: StatusNeedsHuman}, nil)
	if len(actions) != 1 {
		t.Fatalf("want exactly 1 action for needs-human, got %d: %+v", len(actions), actions)
	}
	if actions[0].Action != "retry" {
		t.Fatalf("want action %q, got %q", "retry", actions[0].Action)
	}
}
