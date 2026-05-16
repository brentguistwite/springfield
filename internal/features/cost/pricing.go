// Package cost computes USD spend for agent iterations from token counts and
// surfaces aggregate spend across a batch.
//
// The pricing table is static for v1: each entry pairs an adapter ID and a
// model ID with input/output USD-per-million-token rates sourced from the
// vendor's public pricing page. Runtime fetching (e.g. LiteLLM JSON) is
// deferred to post-v1 — a network dependency adds a failure mode that is
// not worth the complexity for the initial cost-visibility surface.
//
// Sources (last updated 2026-05-15):
//   - Anthropic Claude pricing: https://www.anthropic.com/pricing
//   - OpenAI / Codex pricing:   https://openai.com/api/pricing
//
// When pricing for a given adapter/model pair is unknown, Compute returns
// (0, false). Callers must surface unpriced runs to operators (see
// Rollup.UnpricedRuns) rather than silently treating them as $0.
package cost

import (
	"strings"
	"time"
)

// Rate is the per-million-token billing rate for an adapter/model pair.
type Rate struct {
	InputUSDPerMtok  float64
	OutputUSDPerMtok float64
}

// Capture is the per-iteration cost record persisted alongside other
// evidence. JSON tags match the cost.json wire format.
type Capture struct {
	Adapter      string    `json:"adapter"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	CapturedAt   time.Time `json:"captured_at"`
}

// pricingTable maps adapter ID → model ID → rate. Model IDs are matched
// case-insensitively; callers pass the adapter's reported model string.
var pricingTable = map[string]map[string]Rate{
	"claude": {
		// Claude 4.x family (current).
		"claude-opus-4-7":          {InputUSDPerMtok: 15.00, OutputUSDPerMtok: 75.00},
		"claude-opus-4-1":          {InputUSDPerMtok: 15.00, OutputUSDPerMtok: 75.00},
		"claude-sonnet-4-6":        {InputUSDPerMtok: 3.00, OutputUSDPerMtok: 15.00},
		"claude-sonnet-4-5":        {InputUSDPerMtok: 3.00, OutputUSDPerMtok: 15.00},
		"claude-haiku-4-5-20251001": {InputUSDPerMtok: 1.00, OutputUSDPerMtok: 5.00},
	},
	"codex": {
		// OpenAI Codex / GPT-5.x family. Codex CLI reports the underlying
		// model as gpt-5-codex or gpt-5.4 in its output; both share the
		// same input/output rates per the public pricing page.
		"gpt-5.4":      {InputUSDPerMtok: 1.25, OutputUSDPerMtok: 10.00},
		"gpt-5-codex":  {InputUSDPerMtok: 1.25, OutputUSDPerMtok: 10.00},
		"o3":           {InputUSDPerMtok: 2.00, OutputUSDPerMtok: 8.00},
	},
}

// LookupRate returns the Rate for the given adapter/model and reports whether
// a row exists. Unknown pairs return zero Rate and ok=false.
func LookupRate(adapter, model string) (Rate, bool) {
	adapterTable, ok := pricingTable[strings.ToLower(strings.TrimSpace(adapter))]
	if !ok {
		return Rate{}, false
	}
	rate, ok := adapterTable[strings.ToLower(strings.TrimSpace(model))]
	if !ok {
		return Rate{}, false
	}
	return rate, true
}

// Compute returns the USD cost of an iteration that used inTok input tokens
// and outTok output tokens for the given adapter/model. The second return
// value is false when no pricing row exists for the pair (and the cost is
// returned as 0); callers should treat that as an unpriced run.
//
// Zero-token inputs are valid and return (0, true) when the pair is priced —
// distinguishing a real $0 iteration from an unknown-pricing iteration.
func Compute(adapter, model string, inTok, outTok int) (float64, bool) {
	rate, ok := LookupRate(adapter, model)
	if !ok {
		return 0, false
	}
	cost := (float64(inTok)*rate.InputUSDPerMtok + float64(outTok)*rate.OutputUSDPerMtok) / 1_000_000.0
	return cost, true
}
