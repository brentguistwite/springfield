package planreview_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor/planreview"
)

// fakeRunner captures the request and returns a canned result.
type fakeRunner struct {
	gotReq coreruntime.Request
	result coreruntime.Result
}

func (f *fakeRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	f.gotReq = req
	return f.result
}

func TestReviewBuildsPromptAndParsesVerdict(t *testing.T) {
	fr := &fakeRunner{result: coreruntime.Result{
		Agent: agents.AgentCodex,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "looks risky"},
			{Type: coreexec.EventStdout, Data: "<review-verdict>revise</review-verdict>"},
		},
	}}
	out := planreview.Review(context.Background(), planreview.ReviewInput{
		Runner:   fr,
		AgentIDs: []agents.ID{agents.AgentCodex},
		WorkDir:  "/tmp/wt",
		Diff:     "DIFF-BODY",
		Criteria: []string{"AC one"},
	})
	if out.Err != nil {
		t.Fatalf("unexpected err: %v", out.Err)
	}
	if !out.Found || out.Verdict.Class != planreview.VerdictRevise {
		t.Fatalf("verdict = %+v found=%v, want revise", out.Verdict, out.Found)
	}
	if !strings.Contains(fr.gotReq.Prompt, "DIFF-BODY") {
		t.Fatalf("runner did not receive built prompt: %q", fr.gotReq.Prompt)
	}
	if out.Agent != agents.AgentCodex {
		t.Fatalf("agent = %q, want codex", out.Agent)
	}
}

func TestReviewSurfacesRunnerError(t *testing.T) {
	fr := &fakeRunner{result: coreruntime.Result{
		Agent: agents.AgentClaude,
		Err:   errors.New("agent crashed"),
	}}
	out := planreview.Review(context.Background(), planreview.ReviewInput{
		Runner:   fr,
		AgentIDs: []agents.ID{agents.AgentClaude},
		Diff:     "D",
	})
	if out.Err == nil {
		t.Fatal("expected runner error to surface")
	}
	if out.Found {
		t.Fatal("Found must be false when the agent errored")
	}
}

func TestReviewVerdictlessIsNotFound(t *testing.T) {
	fr := &fakeRunner{result: coreruntime.Result{
		Agent:  agents.AgentClaude,
		Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "I have no opinion"}},
	}}
	out := planreview.Review(context.Background(), planreview.ReviewInput{
		Runner: fr, AgentIDs: []agents.ID{agents.AgentClaude}, Diff: "D",
	})
	if out.Err != nil {
		t.Fatalf("verdict-less review is not an error here: %v", out.Err)
	}
	if out.Found {
		t.Fatal("Found must be false when no verdict marker present")
	}
}
