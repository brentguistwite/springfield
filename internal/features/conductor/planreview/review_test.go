package planreview_test

import (
	"context"
	"errors"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor/planreview"
	"springfield/internal/testsupport/fixtures"
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

// Integration: a REAL coreruntime.Runner replays a REAL captured reviewer
// transcript (escaped stream-json). This exercises both gate fixes together:
// BUG-4 (the runner's relaxed ValidateResult accepts the tool-free reviewer)
// and BUG-1 (Review decodes the transcript via the runner's TranscriptDecoder
// before scanning, so the verdict the council's design would have missed in raw
// JSON is found). Parameterized over the two agents whose shapes were captured.
func TestReviewFindsVerdictInRealTranscript(t *testing.T) {
	registry := agents.NewRegistry(claude.New(osexec.LookPath), codex.New(osexec.LookPath), gemini.New(osexec.LookPath))
	cases := []struct {
		agent agents.ID
		dir   string
	}{
		{agents.AgentClaude, "claude"},
		{agents.AgentCodex, "codex"},
	}
	for _, tc := range cases {
		t.Run(string(tc.agent), func(t *testing.T) {
			events := fixtures.LoadEvents(t, filepath.Join("..", "..", "..", "..", "tests", "realcaptures", tc.dir, "reviewer-verdict-pass-no-tools.jsonl"))
			runFn := func(_ context.Context, _ coreexec.Command, _ coreexec.EventHandler) coreexec.Result {
				return coreexec.Result{ExitCode: 0, Events: events}
			}
			runner := coreruntime.NewTestRunner(registry, runFn, func() time.Time { return time.Unix(0, 0) })

			out := planreview.Review(context.Background(), planreview.ReviewInput{
				Runner:   runner,
				AgentIDs: []agents.ID{tc.agent},
				WorkDir:  "/tmp/wt",
				Diff:     "DIFF-BODY",
				Criteria: []string{"AC one"},
			})
			if out.Err != nil {
				t.Fatalf("review errored (BUG-4 would reject the tool-free reviewer): %v", out.Err)
			}
			if !out.Found || out.Verdict.Class != planreview.VerdictPass {
				t.Fatalf("verdict not found in real transcript (BUG-1): found=%v class=%q", out.Found, out.Verdict.Class)
			}
		})
	}
}

// TestReviewForwardsEnvToRunner pins that the slice's port block reaches the
// reviewer process. The reviewer is tool-free by design, but ReviewerRole only
// relaxes validation — it does not sandbox tools; forwarding Env is
// defense-in-depth so a reviewer that binds a port cannot collide with a
// concurrently running slice.
func TestReviewForwardsEnvToRunner(t *testing.T) {
	fr := &fakeRunner{result: coreruntime.Result{
		Agent:  agents.AgentClaude,
		Events: []coreexec.Event{{Type: coreexec.EventStdout, Data: "<review-verdict>pass</review-verdict>"}},
	}}
	env := map[string]string{"SPRINGFIELD_PORT": "42030", "SPRINGFIELD_PORT_RANGE": "42030-42039"}
	planreview.Review(context.Background(), planreview.ReviewInput{
		Runner:   fr,
		AgentIDs: []agents.ID{agents.AgentClaude},
		WorkDir:  "/tmp/wt",
		Diff:     "D",
		Env:      env,
	})
	if fr.gotReq.Env["SPRINGFIELD_PORT"] != "42030" {
		t.Fatalf("reviewer request SPRINGFIELD_PORT = %q, want 42030 (env=%v)", fr.gotReq.Env["SPRINGFIELD_PORT"], fr.gotReq.Env)
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
