package claude_test

import (
	"math"
	"testing"
	"time"

	"springfield/internal/core/agents/claude"
	coreexec "springfield/internal/core/exec"
)

func evt(line string) coreexec.Event {
	return coreexec.Event{Type: coreexec.EventStdout, Data: line}
}

func TestExtractCost_TokenBasedFromMessages(t *testing.T) {
	events := []coreexec.Event{
		evt(`{"type":"system","subtype":"init"}`),
		evt(`{"type":"assistant","message":{"usage":{"input_tokens":1000,"output_tokens":500}}}`),
		evt(`{"type":"assistant","message":{"usage":{"input_tokens":200,"output_tokens":50}}}`),
	}
	got := claude.ExtractCost(events, "claude-sonnet-4-6", time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC))
	if got.InputTokens != 1200 {
		t.Errorf("input_tokens=%d want 1200", got.InputTokens)
	}
	if got.OutputTokens != 550 {
		t.Errorf("output_tokens=%d want 550", got.OutputTokens)
	}
	// sonnet 4.6: 3.0/Mtok input + 15.0/Mtok output
	// 1200*3/1e6 + 550*15/1e6 = 0.0036 + 0.00825 = 0.01185
	want := 0.01185
	if math.Abs(got.CostUSD-want) > 1e-9 {
		t.Errorf("cost_usd=%v want %v", got.CostUSD, want)
	}
	if got.Adapter != "claude" {
		t.Errorf("adapter=%q want claude", got.Adapter)
	}
	if got.Model != "claude-sonnet-4-6" {
		t.Errorf("model=%q", got.Model)
	}
}

func TestExtractCost_ExplicitCostUSDWins(t *testing.T) {
	events := []coreexec.Event{
		evt(`{"type":"assistant","message":{"usage":{"input_tokens":1000000,"output_tokens":0}}}`),
		evt(`{"type":"result","subtype":"success","usage":{"input_tokens":1000000,"output_tokens":0},"total_cost_usd":0.42}`),
	}
	got := claude.ExtractCost(events, "claude-sonnet-4-6", time.Now())
	// Pricing table would yield 1M*3/1e6 = $3.00, but result event's 0.42 wins.
	if math.Abs(got.CostUSD-0.42) > 1e-9 {
		t.Errorf("expected explicit cost 0.42, got %v", got.CostUSD)
	}
	// The result event's usage block is the cumulative total and must
	// REPLACE any accumulated tokens from prior assistant events — not
	// add on top. Pre-fix this would have shown 2_000_000.
	if got.InputTokens != 1_000_000 {
		t.Errorf("input_tokens=%d want 1_000_000 (result event must NOT double-count assistant tokens)", got.InputTokens)
	}
	if got.OutputTokens != 0 {
		t.Errorf("output_tokens=%d want 0", got.OutputTokens)
	}
}

// TestExtractCost_ResultEventTokensWinWithoutCostUSD verifies the same
// no-double-count rule applies even when the result event carries no
// explicit cost — token-based pricing must still see the correct total.
func TestExtractCost_ResultEventTokensWinWithoutCostUSD(t *testing.T) {
	events := []coreexec.Event{
		evt(`{"type":"assistant","message":{"usage":{"input_tokens":500000,"output_tokens":100000}}}`),
		evt(`{"type":"assistant","message":{"usage":{"input_tokens":500000,"output_tokens":100000}}}`),
		// Result's usage is cumulative; without the de-double-count fix
		// this run would persist 2M/400K instead of 1M/200K.
		evt(`{"type":"result","subtype":"success","usage":{"input_tokens":1000000,"output_tokens":200000}}`),
	}
	got := claude.ExtractCost(events, "claude-sonnet-4-6", time.Now())
	if got.InputTokens != 1_000_000 {
		t.Errorf("input_tokens=%d want 1_000_000", got.InputTokens)
	}
	if got.OutputTokens != 200_000 {
		t.Errorf("output_tokens=%d want 200_000", got.OutputTokens)
	}
	// sonnet 4.6: 1M*3/1e6 + 200K*15/1e6 = 3.00 + 3.00 = $6.00
	if math.Abs(got.CostUSD-6.00) > 1e-9 {
		t.Errorf("cost_usd=%v want 6.00", got.CostUSD)
	}
}

// TestExtractCost_MultipleResultEventsTakeMax pins defensive multi-result
// handling: if a stream emits two result events (session restart / retry /
// error+success sequence), the larger usage block and the larger explicit
// cost win, so we never under-count by trusting a later smaller value.
func TestExtractCost_MultipleResultEventsTakeMax(t *testing.T) {
	events := []coreexec.Event{
		evt(`{"type":"result","subtype":"error","usage":{"input_tokens":500000,"output_tokens":100000},"total_cost_usd":0.50}`),
		evt(`{"type":"result","subtype":"success","usage":{"input_tokens":1000000,"output_tokens":200000},"total_cost_usd":1.25}`),
	}
	got := claude.ExtractCost(events, "claude-sonnet-4-6", time.Now())
	if got.InputTokens != 1_000_000 {
		t.Errorf("input_tokens=%d want 1_000_000 (max of two result events)", got.InputTokens)
	}
	if math.Abs(got.CostUSD-1.25) > 1e-9 {
		t.Errorf("cost_usd=%v want 1.25 (max of two result events)", got.CostUSD)
	}

	// Reverse order — earlier event has larger values. Max still wins.
	events = []coreexec.Event{
		evt(`{"type":"result","subtype":"success","usage":{"input_tokens":1000000,"output_tokens":200000},"total_cost_usd":1.25}`),
		evt(`{"type":"result","subtype":"error","usage":{"input_tokens":500000,"output_tokens":100000},"total_cost_usd":0.50}`),
	}
	got = claude.ExtractCost(events, "claude-sonnet-4-6", time.Now())
	if got.InputTokens != 1_000_000 {
		t.Errorf("input_tokens=%d want 1_000_000 (max regardless of order)", got.InputTokens)
	}
	if math.Abs(got.CostUSD-1.25) > 1e-9 {
		t.Errorf("cost_usd=%v want 1.25 (max regardless of order)", got.CostUSD)
	}
}

func TestExtractCost_NoEvents(t *testing.T) {
	got := claude.ExtractCost(nil, "claude-sonnet-4-6", time.Now())
	if got.CostUSD != 0 || got.InputTokens != 0 || got.OutputTokens != 0 {
		t.Errorf("expected zero capture, got %+v", got)
	}
	if got.Adapter != "claude" {
		t.Errorf("adapter=%q want claude", got.Adapter)
	}
}

func TestExtractCost_UnknownModel(t *testing.T) {
	// Pricing table miss → cost stays zero, tokens still recorded.
	events := []coreexec.Event{
		evt(`{"type":"assistant","message":{"usage":{"input_tokens":1000,"output_tokens":500}}}`),
	}
	got := claude.ExtractCost(events, "claude-future-99", time.Now())
	if got.InputTokens != 1000 || got.OutputTokens != 500 {
		t.Errorf("tokens not captured: %+v", got)
	}
	if got.CostUSD != 0 {
		t.Errorf("expected zero cost for unknown model, got %v", got.CostUSD)
	}
}

func TestExtractCost_IgnoresMalformedJSON(t *testing.T) {
	events := []coreexec.Event{
		evt(`not json at all`),
		evt(`{"type":"assistant","message":{"usage":{"input_tokens":100,"output_tokens":50}}}`),
		evt(`{`),
	}
	got := claude.ExtractCost(events, "claude-sonnet-4-6", time.Now())
	if got.InputTokens != 100 || got.OutputTokens != 50 {
		t.Errorf("expected to skip malformed and sum 100/50, got %+v", got)
	}
}

func TestExtractCost_IgnoresStderrEvents(t *testing.T) {
	events := []coreexec.Event{
		{Type: coreexec.EventStderr, Data: `{"type":"assistant","message":{"usage":{"input_tokens":1000,"output_tokens":500}}}`},
		evt(`{"type":"assistant","message":{"usage":{"input_tokens":50,"output_tokens":25}}}`),
	}
	got := claude.ExtractCost(events, "claude-sonnet-4-6", time.Now())
	if got.InputTokens != 50 || got.OutputTokens != 25 {
		t.Errorf("stderr should be ignored, got %+v", got)
	}
}
