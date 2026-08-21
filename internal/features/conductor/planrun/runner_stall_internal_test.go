package planrun

import (
	"context"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/verify"
)

// spyStall records the ordered sequence of Observe/Suppress calls the verify gate
// makes against the shared detector, so a test can prove the gate SUPPRESSES the
// event-quiet verify command and HEARTBEATS fix-iteration agent events — the two
// halves of US-001's "actively-running verify command suppresses classification"
// contract, wired into production rather than only modeled in the stall package.
type spyStall struct{ log []string }

func (s *spyStall) Observe()        { s.log = append(s.log, "observe") }
func (s *spyStall) Suppress(v bool) { s.log = append(s.log, "suppress:"+boolStr(v)) }

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// eventEmittingRunner replays a scripted event stream through the dispatch's
// OnEvent (which the gate wraps to heartbeat the detector) before returning a
// clean pass, so a fix iteration exercises the heartbeat path. seqRunner does not
// invoke OnEvent, so it cannot cover this.
type eventEmittingRunner struct {
	events []coreexec.Event
	calls  int
}

func (r *eventEmittingRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	r.calls++
	for _, e := range r.events {
		if req.OnEvent != nil {
			req.OnEvent(e)
		}
	}
	return coreruntime.Result{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("fixed")}}
}

// TestVerifyGateSuppressesStallAroundCommand pins the production wiring: the gate
// brackets EVERY verify command run with Suppress(true)/Suppress(false) (no
// heartbeat accrues while the event-quiet suite churns) and heartbeats the shared
// detector on each fix-iteration agent event. The exact call ordering proves the
// suppression is entered from the verify gate — not merely present in the stall
// package — and that no Observe ever lands inside a suppressed command window.
func TestVerifyGateSuppressesStallAroundCommand(t *testing.T) {
	spy := &spyStall{}

	// round 1 fails (→ fix), round 2 passes. Each command run records "cmd" into
	// the SAME ordered log so command position is asserted relative to suppression.
	round := 0
	cmd := func(_ context.Context, _ verify.Request) verify.Result {
		round++
		spy.log = append(spy.log, "cmd")
		if round == 1 {
			return verify.Result{ExitCode: 1, Stderr: "FAIL"}
		}
		return verify.Result{ExitCode: 0}
	}
	runner := &eventEmittingRunner{events: []coreexec.Event{stdout("working")}}

	in := vgInput(t, cmd, runner, 3)
	in.Stall = spy
	got := runVerifyGate(in)
	if got.Outcome != verifyPassed {
		t.Fatalf("outcome = %v, want verifyPassed", got.Outcome)
	}

	want := "suppress:true,cmd,suppress:false,observe,suppress:true,cmd,suppress:false"
	if joined := strings.Join(spy.log, ","); joined != want {
		t.Fatalf("stall call sequence = %q, want %q", joined, want)
	}
}

// TestVerifyGateNoStallControllerRunsUnmonitored proves the seam is optional: a
// nil Stall (detection disabled, or a legacy caller) leaves the gate behaving
// exactly as before — the command runs and the outcome is unchanged, no panic.
func TestVerifyGateNoStallControllerRunsUnmonitored(t *testing.T) {
	cmd := &seqCmd{results: []verify.Result{{ExitCode: 0}}}
	in := vgInput(t, cmd.run, &seqRunner{}, 3)
	in.Stall = nil
	if got := runVerifyGate(in); got.Outcome != verifyPassed {
		t.Fatalf("outcome = %v, want verifyPassed with a nil stall controller", got.Outcome)
	}
}

// TestNewVerifyStallDisabledWhenThresholdZero proves the production builder honors
// the disable path: a zero (or negative) threshold yields a nil controller so the
// verify gate runs unmonitored, matching the [stall] "explicit 0 disables" policy.
func TestNewVerifyStallDisabledWhenThresholdZero(t *testing.T) {
	in := SinglePlanInput{}
	for _, th := range []time.Duration{0, -1 * time.Second} {
		c, cancel := in.newVerifyStall("p", t.TempDir(), th, time.Now, context.Background())
		cancel()
		if c != nil {
			t.Fatalf("threshold %s must disable the verify stall detector (got non-nil controller)", th)
		}
	}
}

// TestNewVerifyStallEnabledBuildsController proves a positive threshold wires a
// real detector into the gate in production — the fix the reviewer flagged as
// missing (suppression was previously never entered from the verify gate).
func TestNewVerifyStallEnabledBuildsController(t *testing.T) {
	in := SinglePlanInput{}
	c, cancel := in.newVerifyStall("p", t.TempDir(), time.Minute, time.Now, context.Background())
	defer cancel()
	if c == nil {
		t.Fatal("a positive threshold must build a stall controller for the verify gate")
	}
}
