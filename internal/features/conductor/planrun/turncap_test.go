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
	err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(84)}, 40, false)
	if err == nil {
		t.Fatal("num_turns 84 > cap 40 without honored COMPLETE must synthesize an error")
	}
	if !strings.Contains(err.Error(), planrun.TurnCapExceededReason) {
		t.Fatalf("error must carry %q tag, got %q", planrun.TurnCapExceededReason, err.Error())
	}
	if !strings.Contains(err.Error(), "84") || !strings.Contains(err.Error(), "40") {
		t.Fatalf("error should name observed turns and cap, got %q", err.Error())
	}
}

func TestEnforceTurnCapWithinCapNoError(t *testing.T) {
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(40)}, 40, false); err != nil {
		t.Fatalf("num_turns == cap must not error, got %v", err)
	}
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(12)}, 40, false); err != nil {
		t.Fatalf("num_turns < cap must not error, got %v", err)
	}
}

func TestEnforceTurnCapHonoredCompleteWinsOverCap(t *testing.T) {
	// 999 turns is far over the cap, but the caller confirms COMPLETE was
	// honored (all stories passed): the work is done, so the cap must not
	// fire. Honored-ness is the caller's signal — EnforceTurnCap does not
	// derive it from events because ScanMarkers can't distinguish a honored
	// COMPLETE from a premature one (the bug the explicit parameter fixes).
	evs := resultEvents(999, true)
	if err := planrun.EnforceTurnCap(evs, 40, true); err != nil {
		t.Fatalf("honored COMPLETE must win over the turn cap regardless of num_turns, got %v", err)
	}
}

func TestEnforceTurnCapPrematureCompleteDoesNotDefuse(t *testing.T) {
	// Premature COMPLETE (marker emitted before all stories passed) is signaled
	// by the caller passing completeHonored=false. The cap MUST still fire on
	// over-cap turn counts — this is the dogfood-incident protection (84 turns
	// of thrash, agent emits COMPLETE prematurely, runner ignores marker, cap
	// must still trip rather than letting the iteration loop spin to 50× cap).
	// Adversarial review round 2 R1F3 + R3 caught this; the old EnforceTurnCap
	// internally called ScanMarkers and was defused by ANY COMPLETE marker.
	evs := resultEvents(999, true) // event stream contains COMPLETE, but caller says NOT honored
	err := planrun.EnforceTurnCap(evs, 40, false)
	if err == nil {
		t.Fatal("premature COMPLETE (completeHonored=false) MUST NOT defuse the cap; want non-nil error")
	}
	if !strings.Contains(err.Error(), planrun.TurnCapExceededReason) {
		t.Fatalf("error must carry %q tag, got %q", planrun.TurnCapExceededReason, err.Error())
	}
}

func TestEnforceTurnCapDisabledWhenZero(t *testing.T) {
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(500)}, 0, false); err != nil {
		t.Fatalf("cap of 0 disables enforcement, got %v", err)
	}
}

func TestEnforceTurnCapNoResultEventNoError(t *testing.T) {
	// Agents that never report num_turns (codex, gemini) leave the field at 0
	// and must never be capped by this monitor.
	ev := coreexec.Event{Type: coreexec.EventStdout, Data: "some non-json agent chatter", Time: time.Now()}
	if err := planrun.EnforceTurnCap([]coreexec.Event{ev}, 40, false); err != nil {
		t.Fatalf("absent num_turns must not trip the cap, got %v", err)
	}
}

// TestSinglePlanTurnCapDefusedByLegitimateCompletion exercises the
// iterationWorkComplete closure end-to-end through SinglePlan. The agent
// burns 200 turns (well over the 40-turn cap) but ALSO emits both the
// target story's pass marker AND <promise>COMPLETE</promise>. The
// runtime-layer cap check must invoke the closure planrun supplied, the
// closure must hypothetically apply the matching pass marker, see that all
// stories would then be passed, and return true — defusing the cap. The
// plan must complete successfully, not fail.
//
// Closes the gap surfaced by adversarial review round 1: the runtime-level
// defuse test (TestTurnCapDefusedByWorkCompleteCheck) uses a trivially-true
// callback so the production iterationWorkComplete logic (hypothetical
// pass application + target-story matching + all-stories-passed check) is
// never exercised end-to-end through SinglePlan. A regression in the
// closure logic would slip past the runtime test and only show up in real
// runs.
func TestSinglePlanTurnCapDefusedByLegitimateCompletion(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}
	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()

	// 200 turns is 5× over the 40-turn cap, but the agent legitimately
	// passed US-001 AND emitted COMPLETE — the closure must hypothetically
	// apply the US-001 pass, see every story would then be passed, and
	// return true. resultEvents(200, true) packs COMPLETE into one stdout
	// event and the num_turns terminal into another (separate events so
	// scanNumTurns and ScanMarkers both parse cleanly).
	defused := coreruntime.Result{
		Agent:    agents.AgentClaude,
		Status:   coreruntime.StatusPassed,
		ExitCode: 0,
		Events: append(
			[]coreexec.Event{
				{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass>", Time: time.Now()},
			},
			resultEvents(200, true)...,
		),
	}
	runner := &iterScriptRunner{replies: []coreruntime.Result{defused}}

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

	if res.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed (WorkCompleteCheck closure should have defused the cap); err=%v", res.Status, res.Err)
	}
	if runner.calls != 1 {
		t.Fatalf("expected exactly 1 agent call (no fallback when cap is defused), got %d", runner.calls)
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
