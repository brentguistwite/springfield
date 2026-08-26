package opencode

import (
	"encoding/json"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/cost"
)

// opencodeUsageEvent decodes the cost-bearing slice of `opencode run
// --format json` NDJSON. Only step_finish events carry usage; events
// dual-tag with an outer snake_case type and a part.type, and the outer
// one discriminates (see the transcript notes on ValidateResult).
type opencodeUsageEvent struct {
	Type string             `json:"type"`
	Part *opencodeUsagePart `json:"part"`
}

type opencodeUsagePart struct {
	Cost   float64             `json:"cost"`
	Tokens *opencodeTokenBlock `json:"tokens"`
}

type opencodeTokenBlock struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// ExtractCost computes a cost.Capture for a single opencode iteration from
// its NDJSON events. Every step_finish event carries that step's own usage —
// input/output/reasoning token components plus cache write/read counts (the
// five sum to tokens.total per event, so values are per-step, never
// cumulative) and a provider-computed part.cost in USD. Extraction therefore
// sums across ALL step_finish events with no de-duplication pass.
//
// Token accounting mirrors the claude/codex adapters: cost.Capture records
// bare input/output only, so reasoning and cache{write,read} are not folded
// into either field — claude likewise leaves its stream's
// cache_creation/cache_read_input_tokens uncaptured. Their billing impact is
// not lost: part.cost is provider-computed and already includes it, and that
// reported figure is trusted outright — there are no pricingTable rows for
// opencode (its models span every provider opencode supports; a static table
// cannot price them), exactly like claude's explicit total_cost_usd path.
// A free-model run reports cost 0 with non-zero tokens, which downstream
// rollups count as unpriced rather than free — accepted, since no local rate
// exists to distinguish the two.
//
// Opencode streams carry no model id (unlike claude's system/init event), so
// the configured string stands, as with codex. Empty or undecodable input
// yields a zero-valued Capture (Adapter/Model/CapturedAt set) — that is the
// expected case for runs that produced no stdout, not an error.
func ExtractCost(events []coreexec.Event, model string, now time.Time) cost.Capture {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	capture := cost.Capture{
		Adapter:    string(agents.AgentOpenCode),
		Model:      model,
		CapturedAt: now.UTC(),
	}

	for _, e := range events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var ev opencodeUsageEvent
		if err := json.Unmarshal([]byte(e.Data), &ev); err != nil {
			continue
		}
		if ev.Type != "step_finish" || ev.Part == nil {
			continue
		}
		if t := ev.Part.Tokens; t != nil {
			capture.InputTokens += t.Input
			capture.OutputTokens += t.Output
		}
		capture.CostUSD += ev.Part.Cost
	}
	return capture
}
