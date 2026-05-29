package planreview

import (
	"context"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
)

// AgentRunner is the runtime boundary planreview depends on. The shared
// coreruntime.Runner satisfies it directly; tests substitute a fake. It is
// declared locally (not imported from planrun) so planreview has no dependency
// on planrun — that lets planrun call planreview without an import cycle.
type AgentRunner interface {
	Run(ctx context.Context, req coreruntime.Request) coreruntime.Result
}

// ReviewInput collects everything Review needs. Diff is precomputed by the
// caller (the runner holds Context.BaseRef/Branch); planreview never runs git.
type ReviewInput struct {
	Runner            AgentRunner
	AgentIDs          []agents.ID
	ExecutionSettings agents.ExecutionSettings
	WorkDir           string
	Diff              string
	Criteria          []string
	CustomPrompt      string
	OnEvent           coreexec.EventHandler
}

// ReviewResult is the outcome of one review invocation. Found is false when the
// reviewer emitted no verdict marker; Err is set when the agent run itself
// failed. Prompt is the exact text sent to the agent — exposed so the caller's
// evidence writer can persist the SAME prompt the agent saw (no risk of
// re-build drift when reviewer config changes).
type ReviewResult struct {
	Verdict Verdict
	Found   bool
	Agent   agents.ID
	Events  []coreexec.Event
	Prompt  string
	Err     error
}

// Review builds the reviewer prompt, runs the reviewer agent once via the
// injected runner, and parses the verdict from its stdout. It does NOT loop —
// the fix-loop and budget live in the runner.
func Review(ctx context.Context, in ReviewInput) ReviewResult {
	prompt := BuildReviewPrompt(in.Diff, in.Criteria, in.CustomPrompt)
	res := in.Runner.Run(ctx, coreruntime.Request{
		AgentIDs:          in.AgentIDs,
		Prompt:            prompt,
		WorkDir:           in.WorkDir,
		OnEvent:           in.OnEvent,
		ExecutionSettings: in.ExecutionSettings,
	})
	if res.Err != nil {
		return ReviewResult{Agent: res.Agent, Events: res.Events, Prompt: prompt, Err: res.Err}
	}
	verdict, found := ScanReviewVerdict(res.Events)
	return ReviewResult{Verdict: verdict, Found: found, Agent: res.Agent, Events: res.Events, Prompt: prompt}
}
