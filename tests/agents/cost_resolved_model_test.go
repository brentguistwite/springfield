package agents_test

import (
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/testsupport/fixtures"
)

// A tier alias ("haiku") tells us nothing after the fact about which model
// actually ran. The CLI reports the resolved id in its own stream, so the cost
// capture records that instead of the configured string — otherwise a batch's
// evidence cannot answer "which model produced this diff?".
func TestClaudeExtractCostRecordsResolvedModelFromRealCapture(t *testing.T) {
	events := fixtures.LoadEvents(t, filepath.Join("..", "realcaptures", "claude", "implementer-story-pass.jsonl"))

	got := claude.ExtractCost(events, "haiku", time.Now())

	if got.Model != "claude-haiku-4-5-20251001" {
		t.Fatalf("Model = %q, want the CLI-reported resolved id %q", got.Model, "claude-haiku-4-5-20251001")
	}
}

// Transcripts that never name a model (a run that died before the init event)
// keep the configured string, so the capture still says something useful.
func TestClaudeExtractCostFallsBackToConfiguredModel(t *testing.T) {
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":5}}}`},
	}

	got := claude.ExtractCost(events, "opus", time.Now())

	if got.Model != "opus" {
		t.Fatalf("Model = %q, want configured fallback %q", got.Model, "opus")
	}
}

// Codex defines its own ExtractCost, and the coverage gate keys on the bare
// function name — so the claude capture above would vouch for it unless codex
// is exercised against real bytes too. Codex reports no model in its stream,
// so the configured string stands; the tokens come off a real turn.completed.
func TestCodexExtractCostFromRealCapture(t *testing.T) {
	events := fixtures.LoadEvents(t, filepath.Join("..", "realcaptures", "codex", "reviewer-verdict-pass-no-tools.jsonl"))

	got := codex.ExtractCost(events, "gpt-5.4", time.Now())

	if got.InputTokens != 17300 || got.OutputTokens != 65 {
		t.Fatalf("tokens = %d/%d, want 17300/65 from the real turn.completed usage block",
			got.InputTokens, got.OutputTokens)
	}
	if got.Model != "gpt-5.4" {
		t.Fatalf("Model = %q, want the configured %q", got.Model, "gpt-5.4")
	}
	// gpt-5.4 is $2.50/$15 per Mtok: 17300*2.5/1e6 + 65*15/1e6 = 0.04325 + 0.000975
	if want := 0.044225; got.CostUSD < want-1e-9 || got.CostUSD > want+1e-9 {
		t.Fatalf("CostUSD = %v, want %v", got.CostUSD, want)
	}
}

// An assistant envelope alone is enough; the init event is not required.
func TestClaudeExtractCostResolvesModelFromAssistantEnvelope(t *testing.T) {
	events := []coreexec.Event{
		{Type: coreexec.EventStdout, Data: `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":5}}}`},
	}

	got := claude.ExtractCost(events, "opus", time.Now())

	if got.Model != "claude-opus-5" {
		t.Fatalf("Model = %q, want %q", got.Model, "claude-opus-5")
	}
}
