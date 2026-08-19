package planrun

import (
	"context"
	"errors"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/prd"
)

// seqRunner returns queued results in order, one per Run call. If a test makes
// more Run calls than were scripted, fall back to a generic success with no
// events so the extra call surfaces as a downstream assertion failure rather
// than an `index out of range` panic that obscures the real test gap.
type seqRunner struct {
	results []coreruntime.Result
	calls   int
	reqs    []coreruntime.Request
}

func (s *seqRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	s.reqs = append(s.reqs, req)
	if s.calls >= len(s.results) {
		s.calls++
		return coreruntime.Result{Agent: agents.AgentClaude}
	}
	r := s.results[s.calls]
	s.calls++
	return r
}

func stdout(data string) coreexec.Event {
	return coreexec.Event{Type: coreexec.EventStdout, Data: data}
}

// stubGit is a self-contained Git double for this internal test (fakeGit lives
// in the external planrun_test package and is not visible from here).
// runReviewGate only calls Diff, but the field is typed Git so every interface
// method is stubbed.
type stubGit struct{ diff string }

func (stubGit) IsRepo(string) (bool, error)                    { return true, nil }
func (stubGit) IsDirty(string) (bool, error)                   { return false, nil }
func (stubGit) ResolveRef(string, string) (string, error)      { return "sha", nil }
func (stubGit) CurrentBranch(string) (string, error)           { return "br", nil }
func (stubGit) BranchExists(string, string) (bool, error)      { return true, nil }
func (stubGit) WorktreeListPaths(string) ([]string, error)     { return nil, nil }
func (stubGit) WorktreeAddNewBranch(_, _, _, _ string) error   { return nil }
func (stubGit) WorktreeAddExistingBranch(_, _, _ string) error { return nil }
func (stubGit) Head(string) (string, error)                    { return "head", nil }
func (g stubGit) Diff(_, _ string) (string, error)             { return g.diff, nil }

func gateInput(r AgentRunner, max int) reviewGateInput {
	return reviewGateInput{
		Runner:            r,
		Git:               stubGit{diff: "DIFF"},
		ImplementerAgents: []agents.ID{agents.AgentClaude},
		ReviewConfig:      config.ReviewConfig{MaxReviewIterations: max},
		WorktreeRoot:      "/tmp/wt",
		BaseRef:           "main",
		PRD: prd.PRD{
			ID:          "P",
			UserStories: []prd.UserStory{{ID: "US-1", AcceptanceCriteria: []string{"works"}}},
		},
		EvidenceDir: "",
	}
}

func TestReviewGatePassMergesImmediately(t *testing.T) {
	r := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>pass</review-verdict>")}},
	}}
	got := runReviewGate(gateInput(r, 3))
	if got.Outcome != reviewPassed {
		t.Fatalf("outcome = %v, want reviewPassed", got.Outcome)
	}
	if r.calls != 1 {
		t.Fatalf("expected exactly 1 review call, got %d", r.calls)
	}
}

func TestReviewGateHaltNeedsHuman(t *testing.T) {
	r := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>halt</review-verdict>")}},
	}}
	got := runReviewGate(gateInput(r, 3))
	if got.Outcome != reviewNeedsHuman {
		t.Fatalf("outcome = %v, want reviewNeedsHuman", got.Outcome)
	}
}

func TestReviewGateReviseThenPass(t *testing.T) {
	r := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>revise</review-verdict>")}}, // review 1
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<promise>COMPLETE</promise>")}},             // fix 1
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>pass</review-verdict>")}},   // review 2
	}}
	got := runReviewGate(gateInput(r, 3))
	if got.Outcome != reviewPassed {
		t.Fatalf("outcome = %v, want reviewPassed", got.Outcome)
	}
	if r.calls != 3 {
		t.Fatalf("expected review→fix→review = 3 calls, got %d", r.calls)
	}
}

func TestReviewGateCapExhaustionEscalatesToNeedsHuman(t *testing.T) {
	// max=2: review(revise)→fix→review(revise) hits cap → needs-human.
	r := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>revise</review-verdict>")}}, // review 1
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<promise>COMPLETE</promise>")}},             // fix 1
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>revise</review-verdict>")}}, // review 2 (round==max)
	}}
	got := runReviewGate(gateInput(r, 2))
	if got.Outcome != reviewNeedsHuman {
		t.Fatalf("outcome = %v, want reviewNeedsHuman (cap)", got.Outcome)
	}
}

func TestReviewGateAgentErrorIsErrored(t *testing.T) {
	r := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Err: context.Canceled},
	}}
	got := runReviewGate(gateInput(r, 3))
	if got.Outcome != reviewErrored || got.Err == nil {
		t.Fatalf("outcome = %v err=%v, want reviewErrored with err", got.Outcome, got.Err)
	}
}

// TestReviewGateFixIterationErrorIsErrored pins the fix-iteration agent-error
// path: a successful review verdict of `revise` followed by a fix-iteration
// runner failure must surface as reviewErrored (NOT reviewNeedsHuman) with the
// underlying error wrapped. Without this test a refactor could quietly downgrade
// the fix.Err branch and the gate would silently merge a half-fixed plan.
func TestReviewGateFixIterationErrorIsErrored(t *testing.T) {
	boom := errors.New("fix-iteration boom")
	r := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>revise</review-verdict>")}}, // review 1
		{Agent: agents.AgentClaude, Err: boom}, // fix 1 fails
	}}
	got := runReviewGate(gateInput(r, 3))
	if got.Outcome != reviewErrored {
		t.Fatalf("outcome = %v, want reviewErrored", got.Outcome)
	}
	if got.Err == nil || !errors.Is(got.Err, boom) {
		t.Fatalf("err = %v, want wrap of %v", got.Err, boom)
	}
}

// TestReviewGateCancelledContextErrorsBeforeReviewerCall pins the cooperative
// cancellation guard at the top of the fix-loop. A pre-canceled context must
// surface as reviewErrored with context.Canceled BEFORE any reviewer call —
// this is the SIGINT path threaded through SinglePlanInput.Ctx. Without this
// test the ctx.Err() check could be silently removed and SIGINT during a
// review-gated batch would still run a full review iteration.
func TestReviewGateCancelledContextErrorsBeforeReviewerCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Empty runner — any call would return the seqRunner fallback (success),
	// which would mask a bug where the cancellation guard is skipped.
	r := &seqRunner{}
	in := gateInput(r, 3)
	in.Ctx = ctx
	got := runReviewGate(in)
	if got.Outcome != reviewErrored {
		t.Fatalf("outcome = %v, want reviewErrored", got.Outcome)
	}
	if !errors.Is(got.Err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", got.Err)
	}
	if r.calls != 0 {
		t.Fatalf("cancel guard must fire before any runner call; got %d calls", r.calls)
	}
}

// TestReviewGateFixIterationReceivesPortBlock pins the port-scope guarantee for
// the review fix-loop: the slice's SPRINGFIELD_PORT block must reach the
// fix-iteration agent dispatch (which may start a server or test suite), while
// the read-only reviewer call does NOT get it. Without threading PortEnv the fix
// agent binds default ports and can collide with a concurrently running slice.
func TestReviewGateFixIterationReceivesPortBlock(t *testing.T) {
	r := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>revise</review-verdict>")}}, // review 1
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<promise>COMPLETE</promise>")}},             // fix 1
		{Agent: agents.AgentClaude, Events: []coreexec.Event{stdout("<review-verdict>pass</review-verdict>")}},   // review 2
	}}
	in := gateInput(r, 3)
	in.PortEnv = map[string]string{"SPRINGFIELD_PORT": "42030", "SPRINGFIELD_PORT_RANGE": "42030-42039"}
	got := runReviewGate(in)
	if got.Outcome != reviewPassed {
		t.Fatalf("outcome = %v, want reviewPassed", got.Outcome)
	}
	if len(r.reqs) != 3 {
		t.Fatalf("expected review→fix→review = 3 requests, got %d", len(r.reqs))
	}
	// reqs[0] is the reviewer (read-only, no port block); reqs[1] is the fix agent.
	if r.reqs[0].Env["SPRINGFIELD_PORT"] != "" {
		t.Errorf("reviewer must not receive the port block, got %v", r.reqs[0].Env)
	}
	if r.reqs[1].Env["SPRINGFIELD_PORT"] != "42030" {
		t.Errorf("fix-iteration SPRINGFIELD_PORT = %q, want 42030 (env=%v)", r.reqs[1].Env["SPRINGFIELD_PORT"], r.reqs[1].Env)
	}
}

// TestReviewGateAgentFallbackHonorsReviewConfigAgent verifies the optional
// cross-agent reviewer override: when ReviewConfig.Agent is set, the reviewer
// call uses that ID instead of the implementer agents. (The implementer
// agents remain in play for the fix-iteration.)
func TestReviewGateAgentFallbackHonorsReviewConfigAgent(t *testing.T) {
	r := &seqRunner{results: []coreruntime.Result{
		{Agent: agents.AgentCodex, Events: []coreexec.Event{stdout("<review-verdict>pass</review-verdict>")}},
	}}
	in := gateInput(r, 3)
	in.ReviewConfig.Agent = string(agents.AgentCodex)
	got := runReviewGate(in)
	if got.Outcome != reviewPassed {
		t.Fatalf("outcome = %v, want reviewPassed", got.Outcome)
	}
	if len(r.reqs) != 1 {
		t.Fatalf("expected 1 review request, got %d", len(r.reqs))
	}
	if len(r.reqs[0].AgentIDs) != 1 || r.reqs[0].AgentIDs[0] != agents.AgentCodex {
		t.Fatalf("review request AgentIDs = %v, want [codex]", r.reqs[0].AgentIDs)
	}
}
