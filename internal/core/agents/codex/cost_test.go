package codex_test

import (
	"math"
	"testing"
	"time"

	"springfield/internal/core/agents/codex"
	coreexec "springfield/internal/core/exec"
)

func evt(line string) coreexec.Event {
	return coreexec.Event{Type: coreexec.EventStdout, Data: line}
}

func TestExtractCost_TopLevelUsage(t *testing.T) {
	events := []coreexec.Event{
		evt(`{"type":"task.completed","usage":{"input_tokens":1000000,"output_tokens":200000}}`),
	}
	got := codex.ExtractCost(events, "gpt-5.4", time.Now())
	// gpt-5.4: 2.50/Mtok input + 15/Mtok output
	// 1_000_000*2.50/1e6 + 200_000*15/1e6 = 2.50 + 3.00 = 5.50
	want := 5.50
	if math.Abs(got.CostUSD-want) > 1e-9 {
		t.Errorf("cost_usd=%v want %v", got.CostUSD, want)
	}
	if got.InputTokens != 1_000_000 || got.OutputTokens != 200_000 {
		t.Errorf("tokens not summed: %+v", got)
	}
	if got.Adapter != "codex" || got.Model != "gpt-5.4" {
		t.Errorf("adapter/model mismatch: %+v", got)
	}
}

func TestExtractCost_ItemNestedUsage(t *testing.T) {
	events := []coreexec.Event{
		evt(`{"type":"item.completed","item":{"type":"turn","usage":{"input_tokens":2000,"output_tokens":1000}}}`),
	}
	got := codex.ExtractCost(events, "gpt-5.4", time.Now())
	if got.InputTokens != 2000 || got.OutputTokens != 1000 {
		t.Errorf("expected token sum from item.usage, got %+v", got)
	}
}

func TestExtractCost_NoEvents(t *testing.T) {
	got := codex.ExtractCost(nil, "gpt-5.4", time.Now())
	if got.Adapter != "codex" {
		t.Errorf("adapter=%q want codex", got.Adapter)
	}
	if got.CostUSD != 0 || got.InputTokens != 0 {
		t.Errorf("expected zero capture, got %+v", got)
	}
}

func TestExtractCost_UnknownModel(t *testing.T) {
	events := []coreexec.Event{
		evt(`{"type":"task.completed","usage":{"input_tokens":1000,"output_tokens":500}}`),
	}
	got := codex.ExtractCost(events, "gpt-future", time.Now())
	if got.CostUSD != 0 {
		t.Errorf("expected zero cost for unknown model, got %v", got.CostUSD)
	}
	if got.InputTokens != 1000 {
		t.Errorf("expected token capture even for unknown model, got %+v", got)
	}
}

func TestExtractCost_IgnoresStderr(t *testing.T) {
	events := []coreexec.Event{
		{Type: coreexec.EventStderr, Data: `{"usage":{"input_tokens":99999,"output_tokens":99999}}`},
	}
	got := codex.ExtractCost(events, "gpt-5.4", time.Now())
	if got.InputTokens != 0 {
		t.Errorf("stderr usage must be ignored, got %+v", got)
	}
}
