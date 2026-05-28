package planrun

import (
	"context"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/prd"
)

// seqRunner returns queued results in order, one per Run call.
type seqRunner struct {
	results []coreruntime.Result
	calls   int
	reqs    []coreruntime.Request
}

func (s *seqRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	s.reqs = append(s.reqs, req)
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
