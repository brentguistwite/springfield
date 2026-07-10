package conductor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestPlanActivityJSONRoundTrip pins the in-flight Activity contract: every
// field survives a marshal/unmarshal cycle unchanged, so a persisted running
// plan's progress signal is read back exactly.
func TestPlanActivityJSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 7, 10, 12, 30, 0, 0, time.UTC)
	in := &PlanState{
		Status: StatusRunning,
		Activity: &PlanActivity{
			Phase:     "reviewing",
			Detail:    "review round 2",
			Round:     2,
			UpdatedAt: ts,
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out PlanState
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Activity == nil {
		t.Fatal("Activity dropped on round-trip")
	}
	got := *out.Activity
	if got.Phase != "reviewing" || got.Detail != "review round 2" || got.Round != 2 {
		t.Fatalf("Activity scalar fields mismatched: %+v", got)
	}
	if !got.UpdatedAt.Equal(ts) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, ts)
	}
}

// TestPlanActivityOmittedWhenNil locks the nil-omit half of the contract: a
// plan with no Activity carries no "activity" key at all, so a never-run plan's
// durable record stays clean.
func TestPlanActivityOmittedWhenNil(t *testing.T) {
	raw, err := json.Marshal(&PlanState{Status: StatusPending})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "activity") {
		t.Fatalf("nil Activity must be omitted; got %s", raw)
	}
}

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
