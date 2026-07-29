// Package cost computes USD spend for agent iterations from token counts and
// surfaces aggregate spend across a batch.
//
// The pricing table is static for v1: each entry pairs an adapter ID and a
// model ID with input/output USD-per-million-token rates sourced from the
// vendor's public pricing page. Runtime fetching (e.g. LiteLLM JSON) is
// deferred to post-v1 — a network dependency adds a failure mode that is
// not worth the complexity for the initial cost-visibility surface.
//
// Sources (last updated 2026-07-17):
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
	// BatchID stamps the capture with the batch it was produced under so a
	// rollup can be scoped to one batch. Evidence dirs are keyed per plan,
	// not per batch, and the conductor reuses plan IDs with iteration
	// counters restarting at 1 — so a stale iter-N from an earlier batch can
	// survive best-effort archive cleanup into a reused plan dir. Scoping the
	// rollup by this field, rather than by plan-key path, is the only way to
	// exclude that leakage. Empty for captures written outside a named batch
	// (the single-plan-unit path); those are treated as unscoped.
	BatchID string `json:"batch_id,omitempty"`
}

// pricingTable maps adapter ID → model ID → rate. Model IDs are matched
// case-insensitively; callers pass the adapter's reported model string.
// Claude has no rows here: claudeTiers prices the whole vendor by family. Add
// one only for a model whose rate differs from its tier.
var pricingTable = map[string]map[string]Rate{
	"codex": {
		// OpenAI Codex / GPT-5.x family, per the public pricing page.
		"gpt-5.5":      {InputUSDPerMtok: 5.00, OutputUSDPerMtok: 30.00},
		"gpt-5.4":      {InputUSDPerMtok: 2.50, OutputUSDPerMtok: 15.00},
		"gpt-5.4-mini": {InputUSDPerMtok: 0.75, OutputUSDPerMtok: 4.50},
	},
}

// claudeTiers prices a Claude model from its tier rather than an exact id, so
// a newly released model is priced on day one without a Springfield change.
// Each entry matches the bare CLI alias ("opus") and every versioned id in that
// family ("claude-opus-5", "claude-opus-4-8", ...).
//
// This is a fallback in two senses. An exact pricingTable row wins over it, and
// the whole table is only consulted when the claude CLI reports no
// total_cost_usd on its result event — which in practice means runs that died
// before finishing. A tier that reprices across generations (Opus 3 was $15/$75
// before the 4.x line landed at $5/$25) needs the old ids added as exact rows.
var claudeTiers = []struct {
	tier string
	rate Rate
}{
	{"fable", Rate{InputUSDPerMtok: 10.00, OutputUSDPerMtok: 50.00}},
	{"opus", Rate{InputUSDPerMtok: 5.00, OutputUSDPerMtok: 25.00}},
	{"sonnet", Rate{InputUSDPerMtok: 3.00, OutputUSDPerMtok: 15.00}},
	{"haiku", Rate{InputUSDPerMtok: 1.00, OutputUSDPerMtok: 5.00}},
}

// lookupClaudeTier resolves model against the tier table. It matches the bare
// alias exactly and versioned ids on the "claude-<tier>-" prefix; the trailing
// hyphen keeps a hypothetical "claude-opusplan-x" out of the opus tier.
func lookupClaudeTier(model string) (Rate, bool) {
	for _, t := range claudeTiers {
		if model == t.tier || strings.HasPrefix(model, "claude-"+t.tier+"-") {
			return t.rate, true
		}
	}
	return Rate{}, false
}

// LookupRate returns the Rate for the given adapter/model and reports whether
// one is known. Unknown pairs return zero Rate and ok=false.
//
// Claude falls back to tier pricing when no exact row matches, so an id for a
// model that shipped after this build still prices correctly. Other adapters
// are exact-match only — their ids carry no tier to infer from.
func LookupRate(adapter, model string) (Rate, bool) {
	adapterID := strings.ToLower(strings.TrimSpace(adapter))
	modelID := strings.ToLower(strings.TrimSpace(model))

	// Indexing a missing adapter yields a nil map, and indexing that is a miss
	// rather than a panic — so an adapter priced purely by tier needs no entry.
	if rate, ok := pricingTable[adapterID][modelID]; ok {
		return rate, true
	}
	if adapterID == "claude" {
		return lookupClaudeTier(modelID)
	}
	return Rate{}, false
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
