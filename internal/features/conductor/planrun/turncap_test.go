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

// resultEvent builds a stream-json terminal result event carrying num_turns,
// mirroring claude's --output-format stream-json output.
func resultEvent(numTurns int, complete bool) coreexec.Event {
	data := fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"num_turns":%d}`, numTurns)
	if complete {
		data = `{"type":"assistant","message":{"content":[{"type":"text","text":"<promise>COMPLETE</promise>"}]}}` + "\n" + data
	}
	return coreexec.Event{Type: coreexec.EventStdout, Data: data, Time: time.Now()}
}

func TestEnforceTurnCapOverCapWithoutCompleteFails(t *testing.T) {
	err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(84, false)}, 40)
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
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(40, false)}, 40); err != nil {
		t.Fatalf("num_turns == cap must not error, got %v", err)
	}
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(12, false)}, 40); err != nil {
		t.Fatalf("num_turns < cap must not error, got %v", err)
	}
}

func TestEnforceTurnCapCompleteWinsOverCap(t *testing.T) {
	// 999 turns is far over the cap, but COMPLETE was emitted: the work is done,
	// so the cap must not fire.
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(999, true)}, 40); err != nil {
		t.Fatalf("COMPLETE must win over the turn cap regardless of num_turns, got %v", err)
	}
}

func TestEnforceTurnCapDisabledWhenZero(t *testing.T) {
	if err := planrun.EnforceTurnCap([]coreexec.Event{resultEvent(500, false)}, 0); err != nil {
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
		Events:   []coreexec.Event{resultEvent(84, false)},
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
