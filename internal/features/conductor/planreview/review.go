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

// assistantTextDecoder is the OPTIONAL capability a runner may provide to decode
// an agent's stream-json transcript into plain reviewer text (real newlines),
// so the verdict scanner matches its anchored regex against decoded text rather
// than the escaped JSON line where it never would (BUG-1). The shared
// coreruntime.Runner implements it (it owns the agent registry); a runner that
// does not falls back to scanning the raw events, which still works for the
// plain-text transcripts used in tests and any non-JSON CLI.
type assistantTextDecoder interface {
	AssistantText(agent agents.ID, events []coreexec.Event) string
}

// verdictScanEvents returns the events ScanReviewVerdict should scan: a single
// synthesized stdout event carrying the decoded reviewer text when the runner
// can decode it, else the raw events unchanged.
func verdictScanEvents(runner AgentRunner, agent agents.ID, events []coreexec.Event) []coreexec.Event {
	dec, ok := runner.(assistantTextDecoder)
	if !ok {
		return events
	}
	text := dec.AssistantText(agent, events)
	if text == "" {
		return events
	}
	return []coreexec.Event{{Type: coreexec.EventStdout, Data: text}}
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
		// Reviewer is tool-free by design (reasons over the inline diff); relax
		// ValidateResult's tool-action contract so a clean verdict-only run is
		// not rejected before the verdict scanner sees it.
		ReviewerRole: true,
	})
	if res.Err != nil {
		return ReviewResult{Agent: res.Agent, Events: res.Events, Prompt: prompt, Err: res.Err}
	}
	verdict, found := ScanReviewVerdict(verdictScanEvents(in.Runner, res.Agent, res.Events))
	return ReviewResult{Verdict: verdict, Found: found, Agent: res.Agent, Events: res.Events, Prompt: prompt}
}
