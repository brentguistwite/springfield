package planrun_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/prd"
)

// resultEvents builds a stream-json transcript: one terminal result event
// carrying num_turns, optionally preceded by a COMPLETE assistant event. Each
// JSON object is its OWN coreexec.Event — packing two JSON objects into one
// Event.Data string breaks json.Unmarshal in [scanNumTurns], which would make
// the cap silently never see the turn count and let tests pass for the wrong
// reason (e.g. a COMPLETE-wins-over-cap test that only "passes" because
// num_turns was never parsed).
func resultEvents(numTurns int, complete bool) []coreexec.Event {
	evs := []coreexec.Event{}
	if complete {
		evs = append(evs, coreexec.Event{
			Type: coreexec.EventStdout,
			Data: `{"type":"assistant","message":{"content":[{"type":"text","text":"<promise>COMPLETE</promise>"}]}}`,
			Time: time.Now(),
		})
	}
	evs = append(evs, coreexec.Event{
		Type: coreexec.EventStdout,
		Data: fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"num_turns":%d}`, numTurns),
		Time: time.Now(),
	})
	return evs
}

// resultEvent is the single-event form for tests that don't need a COMPLETE
// marker (over-cap, within-cap, no-result). Kept as a thin wrapper so the
// existing callsites stay one-liners.
func resultEvent(numTurns int) coreexec.Event {
	return resultEvents(numTurns, false)[0]
}

func TestEnforceTurnCapOverCapWithoutCompleteFails(t *testing.T) {
	err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(84)}, 40)
	if err == nil {
		t.Fatal("num_turns 84 > cap 40 without COMPLETE must synthesize an error")
	}
	if !strings.Contains(err.Error(), planrun.TurnCapExceededReason) {
		t.Fatalf("error must carry %q tag, got %q", planrun.TurnCapExceededReason, err.Error())
	}
	if !strings.Contains(err.Error(), "84") || !strings.Contains(err.Error(), "40") {
		t.Fatalf("error should name observed turns and cap, got %q", err.Error())
	}
}

func TestEnforceTurnCapWithinCapNoError(t *testing.T) {
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(40)}, 40); err != nil {
		t.Fatalf("num_turns == cap must not error, got %v", err)
	}
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(12)}, 40); err != nil {
		t.Fatalf("num_turns < cap must not error, got %v", err)
	}
}

func TestEnforceTurnCapCompleteWinsOverCap(t *testing.T) {
	// 999 turns is far over the cap, but COMPLETE was emitted: the work is done,
	// so the cap must not fire. The COMPLETE event and the result event MUST be
	// separate Events — packing them into a single Event.Data string breaks the
	// num_turns parse, and the test would pass even with the guard removed.
	evs := resultEvents(999, true)
	if err := planrun.EnforceTurnCap(evs, 40); err != nil {
		t.Fatalf("COMPLETE must win over the turn cap regardless of num_turns, got %v", err)
	}
	// Sanity: without the COMPLETE event, the same 999-turn result MUST trip
	// the cap. This locks in that the previous assertion is exercising the
	// COMPLETE guard, not a parse failure.
	if err := planrun.EnforceTurnCap(evs[1:], 40); err == nil {
		t.Fatal("999 turns without COMPLETE must trip the cap (sanity check that the prior test exercises the guard, not a parse failure)")
	}
}

func TestEnforceTurnCapDisabledWhenZero(t *testing.T) {
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(500)}, 0); err != nil {
		t.Fatalf("cap of 0 disables enforcement, got %v", err)
	}
}

func TestEnforceTurnCapNoResultEventNoError(t *testing.T) {
	// Agents that never report num_turns (codex, gemini) leave the field at 0
	// and must never be capped by this monitor.
	ev := coreexec.Event{Type: coreexec.EventStdout, Data: "some non-json agent chatter", Time: time.Now()}
	if err := planrun.EnforceTurnCap([]coreexec.Event{ev}, 40); err != nil {
		t.Fatalf("absent num_turns must not trip the cap, got %v", err)
	}
}

// TestSinglePlanTripsTurnCap pins the loop wiring: a clean-exiting iteration
// that reports more turns than the cap without completing fails the plan with
// the [planrun.TurnCapExceededReason] tag.
func TestSinglePlanTripsTurnCap(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}
	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()

	thrash := coreruntime.Result{
		Agent:    agents.AgentClaude,
		Status:   coreruntime.StatusPassed,
		ExitCode: 0,
		Events:   []coreexec.Event{resultEvent(84)},
	}
	runner := &iterScriptRunner{replies: []coreruntime.Result{thrash}}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:              project,
		ControlRoot:          root,
		WorktreeBase:         ".worktrees",
		AgentIDs:             []agents.ID{agents.AgentClaude},
		Runner:               runner,
		Manager:              &planrun.Manager{Git: g},
		ProjectRoot:          root,
		MaxTurnsPerIteration: 40,
	})

	if res.Status != conductor.StatusFailed {
		t.Fatalf("status = %s, want failed", res.Status)
	}
	if res.Reason != planrun.TurnCapExceededReason {
		t.Fatalf("reason = %q, want %q", res.Reason, planrun.TurnCapExceededReason)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), planrun.TurnCapExceededReason) {
		t.Fatalf("err = %v, want one tagged %q", res.Err, planrun.TurnCapExceededReason)
	}
	// The breaker must fire on the FIRST over-cap iteration, not spin.
	if runner.calls != 1 {
		t.Fatalf("expected exactly 1 agent call before the cap tripped, got %d", runner.calls)
	}
}
