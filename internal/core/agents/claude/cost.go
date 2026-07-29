package claude

import (
	"encoding/json"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/cost"
)

// claudeUsageEvent matches the subset of claude --output-format stream-json
// fields that carry token usage and end-of-run cost info. Both the assistant
// message envelope ({"type":"assistant","message":{"usage":{...}}}) and the
// terminal result event ({"type":"result","usage":{...},"total_cost_usd":N})
// share the inner usage shape; we decode permissively so missing fields fall
// back to zero rather than failing the whole extraction.
type claudeUsageEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	// Top-level on result events.
	TotalCostUSD float64           `json:"total_cost_usd"`
	CostUSD      float64           `json:"cost_usd"` // legacy alias seen on some claude versions
	Usage        *claudeUsageBlock `json:"usage"`

	// Top-level on the system/init event: the concrete model the session
	// resolved to, which is how a tier alias becomes a specific id.
	Model string `json:"model"`

	// Nested under message for assistant / message_start events.
	Message struct {
		Model string            `json:"model"`
		Usage *claudeUsageBlock `json:"usage"`
	} `json:"message"`
}

type claudeUsageBlock struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ExtractCost computes a cost.Capture for a single claude iteration from its
// stream-json events. Token counts sum across any usage block observed in the
// stream. When a terminal result event carries an explicit total_cost_usd,
// that value wins over the pricing-table-derived computation — it includes
// any model-specific discounts (e.g. prompt caching) that the static table
// cannot represent.
//
// The model argument is what the operator configured, which may be a tier
// alias ("opus"). The capture records the concrete id the CLI reports instead
// whenever the stream carries one, so evidence identifies the model that
// actually ran; the configured string is the fallback.
//
// An empty event slice or all-undecodable events return a zero-valued Capture
// (Adapter "claude", CapturedAt set, everything else zero) — that is the
// expected case for runs that produced no stdout, not an error.
func ExtractCost(events []coreexec.Event, model string, now time.Time) cost.Capture {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	capture := cost.Capture{
		Adapter:    string(agents.AgentClaude),
		Model:      model,
		CapturedAt: now.UTC(),
	}

	var explicitCostUSD float64
	sawExplicitCost := false
	var resultTokens *claudeUsageBlock
	var resolvedModel string

	for _, e := range events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var ev claudeUsageEvent
		if err := json.Unmarshal([]byte(e.Data), &ev); err != nil {
			continue
		}
		// First reported id wins — normally the system/init event, with the
		// assistant envelope covering streams that lack one. A mid-run switch
		// (--fallback-model) is not distinguished: tokens are aggregated across
		// the whole run, so no single id would describe them anyway.
		if resolvedModel == "" {
			if ev.Model != "" {
				resolvedModel = ev.Model
			} else if ev.Message.Model != "" {
				resolvedModel = ev.Message.Model
			}
		}
		// Token accumulation is shape-aware to avoid double-counting against
		// claude's stream-json: the terminal `result` event carries a
		// CUMULATIVE usage block that already sums every prior `assistant`
		// event's message.usage. Summing both inflates tokens ~2x for any
		// run that emits a result event. We accumulate only from non-result
		// events (assistant / message_start), then OVERWRITE with the result
		// event's authoritative total when present.
		if ev.Type == "result" {
			// Multiple result events are unusual but possible (session
			// restart, partial+success, error+retry). Defensive choice:
			// take the MAX of any usage block and any cost field seen so
			// we don't silently under-count if a later result reports a
			// smaller value than an earlier one.
			if ev.Usage != nil {
				if resultTokens == nil || ev.Usage.InputTokens+ev.Usage.OutputTokens > resultTokens.InputTokens+resultTokens.OutputTokens {
					resultTokens = ev.Usage
				}
			}
			candidate := ev.TotalCostUSD
			if candidate == 0 {
				candidate = ev.CostUSD
			}
			if candidate > explicitCostUSD {
				explicitCostUSD = candidate
				sawExplicitCost = true
			}
			continue
		}
		if usage := ev.Usage; usage != nil {
			capture.InputTokens += usage.InputTokens
			capture.OutputTokens += usage.OutputTokens
		}
		if usage := ev.Message.Usage; usage != nil {
			capture.InputTokens += usage.InputTokens
			capture.OutputTokens += usage.OutputTokens
		}
	}
	if resultTokens != nil {
		capture.InputTokens = resultTokens.InputTokens
		capture.OutputTokens = resultTokens.OutputTokens
	}
	if resolvedModel != "" {
		capture.Model = resolvedModel
	}

	if sawExplicitCost {
		capture.CostUSD = explicitCostUSD
		return capture
	}
	if priced, ok := cost.Compute(string(agents.AgentClaude), capture.Model, capture.InputTokens, capture.OutputTokens); ok {
		capture.CostUSD = priced
	}
	return capture
}
