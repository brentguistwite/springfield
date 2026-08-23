package opencode_test

import (
	"math"
	"testing"
	"time"

	"springfield/internal/core/agents/opencode"
	coreexec "springfield/internal/core/exec"
)

func evt(line string) coreexec.Event {
	return coreexec.Event{Type: coreexec.EventStdout, Data: line}
}

func TestExtractCost_SumsCostAndTokensAcrossStepFinishes(t *testing.T) {
	events := []coreexec.Event{
		evt(`{"type":"step_start","part":{"type":"step-start"}}`),
		evt(`{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":100,"input":80,"output":15,"reasoning":5,"cache":{"write":0,"read":0}},"cost":0.05}}`),
		evt(`{"type":"text","part":{"type":"text","text":"working","time":{"start":1,"end":2}}}`),
		evt(`{"type":"step_finish","part":{"type":"step-finish","tokens":{"total":200,"input":50,"output":30,"reasoning":10,"cache":{"write":90,"read":20}},"cost":0.07}}`),
	}
	got := opencode.ExtractCost(events, "openai/gpt-5.4", time.Now())
	if want := 0.12; math.Abs(got.CostUSD-want) > 1e-9 {
		t.Errorf("CostUSD=%v want %v (sum of part.cost across step_finish events)", got.CostUSD, want)
	}
	// Reasoning and cache{write,read} components stay out of the capture,
	// mirroring how claude/codex record bare input/output only.
	if got.InputTokens != 130 || got.OutputTokens != 45 {
		t.Errorf("tokens=%d/%d want 130/45", got.InputTokens, got.OutputTokens)
	}
	if got.Adapter != "opencode" || got.Model != "openai/gpt-5.4" {
		t.Errorf("adapter/model mismatch: %+v", got)
	}
}

func TestExtractCost_NoEvents(t *testing.T) {
	got := opencode.ExtractCost(nil, "openai/gpt-5.4", time.Now())
	if got.Adapter != "opencode" {
		t.Errorf("adapter=%q want opencode", got.Adapter)
	}
	if got.CostUSD != 0 || got.InputTokens != 0 || got.OutputTokens != 0 {
		t.Errorf("expected zero capture, got %+v", got)
	}
}

func TestExtractCost_IgnoresNonStepFinishAndNonStdoutEvents(t *testing.T) {
	events := []coreexec.Event{
		// step_start parts carry no tokens/cost; a buggy decode must not
		// pick them up.
		evt(`{"type":"step_start","part":{"tokens":{"input":999},"cost":9}}`),
		evt(`{"type":"step_finish","part":{"type":"step-finish","tokens":{"input":10,"output":2},"cost":0.01}}`),
		{Type: coreexec.EventStderr, Data: `{"type":"step_finish","part":{"tokens":{"input":777,"output":777},"cost":77}}`},
		evt(`not json`),
	}
	got := opencode.ExtractCost(events, "m", time.Now())
	if got.InputTokens != 10 || got.OutputTokens != 2 {
		t.Errorf("only stdout step_finish events may count, got %+v", got)
	}
	if want := 0.01; math.Abs(got.CostUSD-want) > 1e-9 {
		t.Errorf("CostUSD=%v want %v", got.CostUSD, want)
	}
}
