package statusview_test

import (
	"encoding/json"
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/statusview"
)

func activityInput(ps *conductor.PlanState, live bool) statusview.ActiveInput {
	b := batch.Batch{
		ID:      "b",
		Title:   "T",
		PlanIDs: []string{"p1"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"p1"}}},
	}
	return statusview.ActiveInput{
		Batch: b,
		Run:   batch.Run{ActiveBatchID: "b"},
		State: &conductor.State{Plans: map[string]*conductor.PlanState{"p1": ps}},
		Units: []conductor.PlanUnit{{ID: "p1", Title: "Plan 1"}},
		Live:  live,
	}
}

// TestActive_ActivityPresentWhenRunning locks the running-plan projection: a
// live running plan with a stamped Activity surfaces an ActivityView carrying
// the same fields.
func TestActive_ActivityPresentWhenRunning(t *testing.T) {
	ts := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	ps := &conductor.PlanState{
		Status: conductor.StatusRunning,
		Activity: &conductor.PlanActivity{
			Phase:     "implementing",
			Detail:    "story 3",
			Round:     3,
			UpdatedAt: ts,
		},
	}
	v := statusview.Active(activityInput(ps, true))
	got := v.Plans[0].Activity
	if got == nil {
		t.Fatal("Activity must be present for a running plan")
	}
	if got.Phase != "implementing" || got.Detail != "story 3" || got.Round != 3 {
		t.Fatalf("Activity fields mismatched: %+v", got)
	}
	if !got.UpdatedAt.Equal(ts) {
		t.Fatalf("UpdatedAt = %v, want %v", got.UpdatedAt, ts)
	}
}

// TestActive_ActivityNullWhenNotRunning is the truthfulness guard: a
// non-running plan projects null Activity EVEN when PlanState still carries a
// stale Activity from a prior phase — a stale value must never leak.
func TestActive_ActivityNullWhenNotRunning(t *testing.T) {
	stale := &conductor.PlanActivity{Phase: "reviewing", Round: 1, UpdatedAt: time.Now()}
	cases := map[string]*conductor.PlanState{
		"pending":   {Status: conductor.StatusPending, Activity: stale},
		"completed": {Status: conductor.StatusCompleted, Activity: stale},
		"failed":    {Status: conductor.StatusFailed, Activity: stale},
	}
	for name, ps := range cases {
		t.Run(name, func(t *testing.T) {
			v := statusview.Active(activityInput(ps, true))
			if got := v.Plans[0].Activity; got != nil {
				t.Fatalf("Activity must be null for a %s plan, got %+v", name, got)
			}
			raw, err := json.Marshal(v.Plans[0])
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !hasNullActivity(raw) {
				t.Fatalf("plan JSON must emit explicit null activity; got %s", raw)
			}
		})
	}
}

// TestActive_ActivityNilWhenRunningUnstamped keeps the projection silent rather
// than inventing a phase: a running plan with no Activity yet stays null.
func TestActive_ActivityNilWhenRunningUnstamped(t *testing.T) {
	v := statusview.Active(activityInput(&conductor.PlanState{Status: conductor.StatusRunning}, true))
	if got := v.Plans[0].Activity; got != nil {
		t.Fatalf("unstamped running plan must project null Activity, got %+v", got)
	}
}

// TestActive_ActivityNullWhenStalled guards the started-but-dead case: a plan
// left "running" with no live process owning the lock composes to "stalled", so
// its Activity must not surface (the coarse phase can no longer be trusted).
func TestActive_ActivityNullWhenStalled(t *testing.T) {
	ps := &conductor.PlanState{
		Status:   conductor.StatusRunning,
		Activity: &conductor.PlanActivity{Phase: "verifying", Round: 1, UpdatedAt: time.Now()},
	}
	v := statusview.Active(activityInput(ps, false))
	if v.Plans[0].Status != statusview.StatusStalled {
		t.Fatalf("precondition: want stalled status, got %q", v.Plans[0].Status)
	}
	if got := v.Plans[0].Activity; got != nil {
		t.Fatalf("stalled plan must project null Activity, got %+v", got)
	}
}

func hasNullActivity(raw []byte) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	v, ok := m["activity"]
	return ok && string(v) == "null"
}
