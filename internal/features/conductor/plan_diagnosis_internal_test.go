package conductor

import (
	"strings"
	"testing"
)

func TestAvailableActionsNeedsHumanOffersRetry(t *testing.T) {
	actions := availableActions(&PlanState{Status: StatusNeedsHuman}, nil)
	if len(actions) != 1 {
		t.Fatalf("want exactly 1 action for needs-human, got %d: %+v", len(actions), actions)
	}
	if actions[0].Action != "retry" {
		t.Fatalf("want action %q, got %q", "retry", actions[0].Action)
	}
	// Default (review) wording — the plan re-enters the pre-merge review gate.
	if !strings.Contains(actions[0].Description, "review") {
		t.Fatalf("review needs-human description must mention review; got %q", actions[0].Description)
	}
}

// TestAvailableActionsVerifyNeedsHumanRetryMentionsVerify pins that a plan
// halted at the verify gate (StatusNeedsHuman + verify-needs-human) still offers
// retry, but with verify-appropriate remediation instead of "address the
// reviewer's findings" — a plan that failed `go test` re-enters the verify gate,
// not the review gate, so the operator must be pointed at the failing command.
func TestAvailableActionsVerifyNeedsHumanRetryMentionsVerify(t *testing.T) {
	actions := availableActions(&PlanState{Status: StatusNeedsHuman, ExitReason: "verify-needs-human"}, nil)
	if len(actions) != 1 {
		t.Fatalf("want exactly 1 action for verify needs-human, got %d: %+v", len(actions), actions)
	}
	if actions[0].Action != "retry" {
		t.Fatalf("want action %q, got %q", "retry", actions[0].Action)
	}
	desc := strings.ToLower(actions[0].Description)
	if !strings.Contains(desc, "verify") {
		t.Fatalf("verify needs-human description must mention verify; got %q", actions[0].Description)
	}
	if strings.Contains(desc, "reviewer") {
		t.Fatalf("verify needs-human description must NOT point at the reviewer; got %q", actions[0].Description)
	}
}
