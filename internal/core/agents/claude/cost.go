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

	// Nested under message for assistant / message_start events.
	Message struct {
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

	for _, e := range events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var ev claudeUsageEvent
		if err := json.Unmarshal([]byte(e.Data), &ev); err != nil {
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
		if ev.Type == "result" {
			if ev.TotalCostUSD > 0 {
				explicitCostUSD = ev.TotalCostUSD
				sawExplicitCost = true
			} else if ev.CostUSD > 0 {
				explicitCostUSD = ev.CostUSD
				sawExplicitCost = true
			}
		}
	}

	if sawExplicitCost {
		capture.CostUSD = explicitCostUSD
		return capture
	}
	if priced, ok := cost.Compute(string(agents.AgentClaude), model, capture.InputTokens, capture.OutputTokens); ok {
		capture.CostUSD = priced
	}
	return capture
}
