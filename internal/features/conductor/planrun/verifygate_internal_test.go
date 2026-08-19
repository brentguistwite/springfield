package planrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/prd"
	"springfield/internal/features/verify"
)

// seqCmd is a scripted verify command-runner double. It returns queued results
// in order, one per call, recording the requests it saw. Extra calls fall back
// to a generic exit-0 success so an over-run surfaces as a downstream assertion
// miss rather than an index panic (mirrors seqRunner's contract).
type seqCmd struct {
	results []verify.Result
	calls   int
	reqs    []verify.Request
}

func (s *seqCmd) run(_ context.Context, req verify.Request) verify.Result {
	s.reqs = append(s.reqs, req)
	if s.calls >= len(s.results) {
		s.calls++
		return verify.Result{ExitCode: 0}
	}
	r := s.results[s.calls]
	s.calls++
	return r
}

func vgInput(t *testing.T, cmd verifyCommandFunc, agent AgentRunner, max int) verifyGateInput {
	return verifyGateInput{
		Command:           cmd,
		Runner:            agent,
		ImplementerAgents: []agents.ID{agents.AgentClaude},
		VerifyCommand:     "go test ./...",
		Timeout:           20 * time.Minute,
		MaxIterations:     max,
		WorktreeRoot:      "/tmp/wt",
		PRD: prd.PRD{
			ID:          "P",
			UserStories: []prd.UserStory{{ID: "US-1", AcceptanceCriteria: []string{"works"}}},
		},
		// Default to a per-test temp dir, never "". An empty EvidenceDir makes a
		// failing round write filepath.Join("", "verify-iter-N") relative to the
		// package cwd, leaking evidence dirs into the source tree and dirtying the
		// working copy (which would trip Springfield's own dirty-source preflight).
		EvidenceDir: t.TempDir(),
	}
}

func TestVerifyGatePassOnExitZero(t *testing.T) {
	cmd := &seqCmd{results: []verify.Result{{ExitCode: 0}}}
	agent := &seqRunner{} // must never be called
	got := runVerifyGate(vgInput(t, cmd.run, agent, 3))
	if got.Outcome != verifyPassed {
		t.Fatalf("outcome = %v, want verifyPassed", got.Outcome)
	}
	if cmd.calls != 1 {
		t.Fatalf("expected exactly 1 command call, got %d", cmd.calls)
	}
	if agent.calls != 0 {
		t.Fatalf("a passing command must not invoke the fix agent; got %d calls", agent.calls)
	}
}

func TestVerifyGateFailThenFixThenPass(t *testing.T) {
	cmd := &seqCmd{results: []verify.Result{
		{ExitCode: 1, Stderr: "FAIL"}, // round 1: fail
		{ExitCode: 0},                 // round 2: pass after fix
	}}
	agent := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<promise>COMPLETE</promise>")}}, // fix 1
	}}
	got := runVerifyGate(vgInput(t, cmd.run, agent, 3))
	if got.Outcome != verifyPassed {
		t.Fatalf("outcome = %v, want verifyPassed", got.Outcome)
	}
	if cmd.calls != 2 {
		t.Fatalf("expected command→fix→command = 2 command calls, got %d", cmd.calls)
	}
	if agent.calls != 1 {
		t.Fatalf("expected exactly 1 fix-agent call, got %d", agent.calls)
	}
}

// TestVerifyGateFixIterationReceivesPortBlock pins the port-scope guarantee for
// the verify fix-loop: the slice's SPRINGFIELD_PORT block must reach BOTH the
// verify command AND the fix-iteration agent dispatch, so a fix agent that starts
// a server binds the same ports the suite expects and no concurrently running
// slice will touch.
func TestVerifyGateFixIterationReceivesPortBlock(t *testing.T) {
	cmd := &seqCmd{results: []verify.Result{
		{ExitCode: 1, Stderr: "FAIL"}, // round 1: fail → triggers fix
		{ExitCode: 0},                 // round 2: pass after fix
	}}
	agent := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<promise>COMPLETE</promise>")}}, // fix 1
	}}
	in := vgInput(t, cmd.run, agent, 3)
	in.PortEnv = map[string]string{"SPRINGFIELD_PORT": "42030", "SPRINGFIELD_PORT_RANGE": "42030-42039"}
	got := runVerifyGate(in)
	if got.Outcome != verifyPassed {
		t.Fatalf("outcome = %v, want verifyPassed", got.Outcome)
	}
	if len(cmd.reqs) < 1 || cmd.reqs[0].Env["SPRINGFIELD_PORT"] != "42030" {
		t.Errorf("verify command did not receive the port block: %v", cmd.reqs)
	}
	if len(agent.reqs) != 1 {
		t.Fatalf("expected exactly 1 fix-agent request, got %d", len(agent.reqs))
	}
	if agent.reqs[0].Env["SPRINGFIELD_PORT"] != "42030" {
		t.Errorf("fix-iteration SPRINGFIELD_PORT = %q, want 42030 (env=%v)", agent.reqs[0].Env["SPRINGFIELD_PORT"], agent.reqs[0].Env)
	}
}

func TestVerifyGateExhaustionEscalatesToNeedsHuman(t *testing.T) {
	// max=2: fail(round1)→fix→fail(round2==max) → needs-human, no 3rd command.
	cmd := &seqCmd{results: []verify.Result{
		{ExitCode: 1},
		{ExitCode: 1},
		{ExitCode: 1},
	}}
	agent := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("fix")}},
	}}
	got := runVerifyGate(vgInput(t, cmd.run, agent, 2))
	if got.Outcome != verifyNeedsHuman {
		t.Fatalf("outcome = %v, want verifyNeedsHuman (exhaustion)", got.Outcome)
	}
	if cmd.calls != 2 {
		t.Fatalf("cap=2 must run exactly 2 commands, got %d", cmd.calls)
	}
}

func TestVerifyGateAgentErrorIsErrored(t *testing.T) {
	cmd := &seqCmd{results: []verify.Result{{ExitCode: 1}}}
	agent := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Err: errors.New("fix boom")},
	}}
	got := runVerifyGate(vgInput(t, cmd.run, agent, 3))
	if got.Outcome != verifyErrored || got.Err == nil {
		t.Fatalf("outcome = %v err=%v, want verifyErrored with err", got.Outcome, got.Err)
	}
}

func TestVerifyGateLaunchFailureIsErrored(t *testing.T) {
	// A command that could not be launched (res.Err set) is a hard error, NOT a
	// failed round the fix loop can act on — there is nothing to fix.
	boom := errors.New("chdir: no such directory")
	cmd := &seqCmd{results: []verify.Result{{ExitCode: -1, Err: boom}}}
	agent := &seqRunner{}
	got := runVerifyGate(vgInput(t, cmd.run, agent, 3))
	if got.Outcome != verifyErrored {
		t.Fatalf("outcome = %v, want verifyErrored", got.Outcome)
	}
	if !errors.Is(got.Err, boom) {
		t.Fatalf("err = %v, want wrap of %v", got.Err, boom)
	}
	if agent.calls != 0 {
		t.Fatalf("launch failure must not invoke the fix agent; got %d", agent.calls)
	}
}

func TestVerifyGateTwoConsecutiveTimeoutsEscalateEarly(t *testing.T) {
	// max=5, but two back-to-back timeouts short-circuit at round 2 — well
	// before the cap — because a stuck suite will not un-stick on retry.
	cmd := &seqCmd{results: []verify.Result{
		{ExitCode: -1, TimedOut: true}, // round 1 timeout
		{ExitCode: -1, TimedOut: true}, // round 2 timeout
	}}
	agent := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("fix")}},
	}}
	got := runVerifyGate(vgInput(t, cmd.run, agent, 5))
	if got.Outcome != verifyNeedsHuman {
		t.Fatalf("outcome = %v, want verifyNeedsHuman (early timeout)", got.Outcome)
	}
	if cmd.calls != 2 {
		t.Fatalf("two consecutive timeouts must stop at 2 commands (not %d)", cmd.calls)
	}
}

func TestVerifyGateSingleTimeoutDoesNotEscalate(t *testing.T) {
	// A lone timeout must reset the consecutive counter: timeout→fix→exit0 passes
	// rather than being treated as part of a timeout streak.
	cmd := &seqCmd{results: []verify.Result{
		{ExitCode: -1, TimedOut: true}, // round 1 timeout
		{ExitCode: 0},                  // round 2 clean pass
	}}
	agent := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("fix")}},
	}}
	got := runVerifyGate(vgInput(t, cmd.run, agent, 5))
	if got.Outcome != verifyPassed {
		t.Fatalf("outcome = %v, want verifyPassed", got.Outcome)
	}
}

func TestVerifyGateEvidenceWrittenEachRound(t *testing.T) {
	dir := t.TempDir()
	cmd := &seqCmd{results: []verify.Result{
		{ExitCode: 1, Stdout: "round1 out"},
		{ExitCode: 1, Stdout: "round2 out"},
	}}
	agent := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("fix")}},
	}}
	in := vgInput(t, cmd.run, agent, 2)
	in.EvidenceDir = dir
	got := runVerifyGate(in)
	if got.Outcome != verifyNeedsHuman {
		t.Fatalf("outcome = %v, want verifyNeedsHuman", got.Outcome)
	}
	for round := 1; round <= 2; round++ {
		vj := filepath.Join(dir, "verify-iter-"+itoa(round), "verify.json")
		if _, err := os.Stat(vj); err != nil {
			t.Fatalf("expected verify.json for round %d at %s: %v", round, vj, err)
		}
	}
}

func TestVerifyGateCancelledContextErrorsBeforeCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := &seqCmd{}
	agent := &seqRunner{}
	in := vgInput(t, cmd.run, agent, 3)
	in.Ctx = ctx
	got := runVerifyGate(in)
	if got.Outcome != verifyErrored {
		t.Fatalf("outcome = %v, want verifyErrored", got.Outcome)
	}
	if !errors.Is(got.Err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", got.Err)
	}
	if cmd.calls != 0 {
		t.Fatalf("cancel guard must fire before any command run; got %d", cmd.calls)
	}
}

func TestVerifyGateCancelledMidCommandErrorsBeforeFix(t *testing.T) {
	// A context cancelled WHILE the command runs (the caller aborted, e.g. SIGINT)
	// must NOT be treated as a fixable failed round: the gate must surface
	// verifyErrored (propagating ctx.Err) and never dispatch a fix agent against
	// the already-cancelled context — the misclassification that would persist a
	// false verify-needs-human diagnosis for a user abort.
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	cmd := func(_ context.Context, _ verify.Request) verify.Result {
		calls++
		cancel() // caller aborts mid-command
		// The killed command reports as cancelled (mirrors verify.Run's Result).
		return verify.Result{ExitCode: -1, Cancelled: true}
	}
	agent := &seqRunner{} // must never be called
	in := vgInput(t, cmd, agent, 3)
	in.Ctx = ctx
	got := runVerifyGate(in)
	if got.Outcome != verifyErrored {
		t.Fatalf("outcome = %v, want verifyErrored (mid-command cancel is an abort, not a fixable round)", got.Outcome)
	}
	if !errors.Is(got.Err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", got.Err)
	}
	if agent.calls != 0 {
		t.Fatalf("a cancelled context must NOT dispatch a fix agent; got %d calls", agent.calls)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 command call before the cancel guard fired, got %d", calls)
	}
}

func TestVerifyGateLastRoundTimeoutNamesTimeoutInReason(t *testing.T) {
	// max=1: a single round that TIMES OUT exhausts the budget without the
	// two-in-a-row early escalation firing. The exhaustion reason must mention the
	// timeout so it is not silently generic (unlike the early-escalation path).
	cmd := &seqCmd{results: []verify.Result{{ExitCode: -1, TimedOut: true}}}
	agent := &seqRunner{} // budget is 1, no fix iteration
	got := runVerifyGate(vgInput(t, cmd.run, agent, 1))
	if got.Outcome != verifyNeedsHuman {
		t.Fatalf("outcome = %v, want verifyNeedsHuman", got.Outcome)
	}
	if !strings.Contains(got.Reason, "last round timed out") {
		t.Fatalf("reason = %q, want it to mention the last-round timeout", got.Reason)
	}
}

// itoa avoids pulling strconv into the test just for a single small int.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
