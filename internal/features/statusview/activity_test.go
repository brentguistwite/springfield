package statusview_test

import (
	"encoding/json"
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
	"springfield/internal/features/statusview"
)

func activityInput(ps *conductor.PlanState, live bool) statusview.ActiveInput {
	return activityInputWithPRD(ps, live, nil)
}

func activityInputWithPRD(ps *conductor.PlanState, live bool, p *prd.PRD) statusview.ActiveInput {
	b := batch.Batch{
		ID:      "b",
		Title:   "T",
		PlanIDs: []string{"p1"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"p1"}}},
	}
	in := statusview.ActiveInput{
		Batch: b,
		Run:   batch.Run{ActiveBatchID: "b"},
		State: &conductor.State{Plans: map[string]*conductor.PlanState{"p1": ps}},
		Units: []conductor.PlanUnit{{ID: "p1", Title: "Plan 1"}},
		Live:  live,
	}
	if p != nil {
		in.PRDs = map[string]prd.PRD{"p1": *p}
	}
	return in
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

// TestActive_DerivedCurrentStoryFromPRD is the Tier-1 guarantee: with ZERO writes
// to PlanState.Activity, the projection derives the coarse phase's current story
// straight from the persisted prd.json passes — and flipping a story's passes
// moves the derived current story, proving it tracks durable truth (can't go
// stale) rather than a written value.
func TestActive_DerivedCurrentStoryFromPRD(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Priority: 1, Passes: false},
		{ID: "US-002", Priority: 2, Passes: false},
	}}
	ps := &conductor.PlanState{Status: conductor.StatusRunning}

	v := statusview.Active(activityInputWithPRD(ps, true, &p))
	got := v.Plans[0].Activity
	if got == nil {
		t.Fatal("derived Activity must be present for a running plan with a PRD")
	}
	if got.Phase != "implementing" {
		t.Fatalf("derived coarse phase = %q, want implementing", got.Phase)
	}
	if got.Detail != "US-001" {
		t.Fatalf("derived current story = %q, want US-001", got.Detail)
	}
	// ZERO writes: derivation must not mutate the persisted Activity.
	if ps.Activity != nil {
		t.Fatalf("derivation wrote to PlanState.Activity: %+v", ps.Activity)
	}

	// Flip the durable truth: US-001 now passes → current story moves to US-002.
	p.UserStories[0].Passes = true
	v = statusview.Active(activityInputWithPRD(ps, true, &p))
	got = v.Plans[0].Activity
	if got == nil || got.Detail != "US-002" {
		t.Fatalf("after passing US-001, derived current story = %v, want US-002", got)
	}
	if ps.Activity != nil {
		t.Fatalf("derivation wrote to PlanState.Activity: %+v", ps.Activity)
	}
}

// TestActive_DerivedSilentWhenAllStoriesPassed keeps the projection truthful when
// no story is eligible (all passed / blocked): with no written Activity, there is
// no current story to derive, so Activity stays null rather than inventing one.
func TestActive_DerivedSilentWhenAllStoriesPassed(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{{ID: "US-001", Passes: true}}}
	ps := &conductor.PlanState{Status: conductor.StatusRunning}
	v := statusview.Active(activityInputWithPRD(ps, true, &p))
	if got := v.Plans[0].Activity; got != nil {
		t.Fatalf("no eligible story + no write must project null Activity, got %+v", got)
	}
}

// TestActive_SuppressesContradictoryWrite is the contradiction-suppression
// backstop (US-007): a written Activity whose detail names a story that durable
// prd.json shows already PASSED is stale by construction — the runner moved on
// without the write catching up. The projection must drop that written overlay
// (detail AND its fine counter, which belonged to the finished story) and fall
// back to Tier-1 derivation, never surfacing the value durable truth contradicts.
func TestActive_SuppressesContradictoryWrite(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Priority: 1, Passes: true},  // durable truth: done
		{ID: "US-002", Priority: 2, Passes: false}, // durable truth: current
	}}
	// Stale write: claims still implementing US-001 (round 5), which has passed.
	stale := &conductor.PlanActivity{
		Phase:     "implementing",
		Detail:    "US-001",
		Round:     5,
		UpdatedAt: time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC),
	}
	started := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	ps := &conductor.PlanState{Status: conductor.StatusRunning, Activity: stale, StartedAt: started}

	v := statusview.Active(activityInputWithPRD(ps, true, &p))
	got := v.Plans[0].Activity
	if got == nil {
		t.Fatal("suppression must fall back to derived Activity, not drop it entirely")
	}
	if got.Detail == "US-001" {
		t.Fatalf("contradictory detail US-001 must be suppressed, got %q", got.Detail)
	}
	if got.Detail != "US-002" {
		t.Fatalf("must fall back to derived current story US-002, got %q", got.Detail)
	}
	if got.Round == 5 {
		t.Fatal("stale round 5 (belonged to passed US-001) must not carry over to the derived story")
	}
	if !got.UpdatedAt.Equal(started) {
		t.Fatalf("derived fallback must anchor UpdatedAt to StartedAt, got %v", got.UpdatedAt)
	}
	// Suppression is read-only: it must not mutate the persisted stale write.
	if ps.Activity != stale {
		t.Fatal("suppression must not mutate PlanState.Activity")
	}
}

// TestActive_HonestWriteNotSuppressed pins the boundary of the backstop: a write
// whose detail names the CURRENT (not-yet-passed) story is consistent with
// durable truth, so its fine counter must survive — suppression fires only on
// contradiction, not on every written detail.
func TestActive_HonestWriteNotSuppressed(t *testing.T) {
	p := prd.PRD{UserStories: []prd.UserStory{
		{ID: "US-001", Priority: 1, Passes: false},
	}}
	write := &conductor.PlanActivity{Phase: "implementing", Detail: "US-001", Round: 3, UpdatedAt: time.Now()}
	ps := &conductor.PlanState{Status: conductor.StatusRunning, Activity: write}

	v := statusview.Active(activityInputWithPRD(ps, true, &p))
	got := v.Plans[0].Activity
	if got == nil || got.Detail != "US-001" || got.Round != 3 {
		t.Fatalf("honest write for the current story must survive, got %+v", got)
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
