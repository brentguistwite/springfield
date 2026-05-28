package conductor

import "testing"

func TestStatusNeedsHumanIsDistinctTerminalValue(t *testing.T) {
	if StatusNeedsHuman != "needs-human" {
		t.Fatalf("StatusNeedsHuman = %q, want %q", StatusNeedsHuman, "needs-human")
	}
	for _, other := range []PlanStatus{StatusPending, StatusRunning, StatusInterrupted, StatusCompleted, StatusFailed} {
		if StatusNeedsHuman == other {
			t.Fatalf("StatusNeedsHuman collides with %q", other)
		}
	}
}

func TestNeedsHumanIsNotIntegrated(t *testing.T) {
	s := &PlanState{Status: StatusNeedsHuman}
	if s.IsIntegrated() {
		t.Fatal("needs-human plan must not report integrated")
	}
}
