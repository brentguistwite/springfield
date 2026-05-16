package codex

import (
	"encoding/json"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/cost"
)

// codexUsageEvent decodes the token-bearing slice of codex --json output.
// Codex emits stream events with shape {"type":"...","item":{...}} for work
// items, and may emit a terminal task/turn event with a `usage` block at the
// top level (or nested under `item`). We decode permissively so missing
// fields fall through as zero rather than aborting extraction.
type codexUsageEvent struct {
	Type  string            `json:"type"`
	Usage *codexUsageBlock  `json:"usage"`
	Item  *codexUsageItem   `json:"item"`
}

type codexUsageItem struct {
	Type  string           `json:"type"`
	Usage *codexUsageBlock `json:"usage"`
}

type codexUsageBlock struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ExtractCost computes a cost.Capture for a single codex iteration. Codex
// does not expose a USD field; cost is always derived from the pricing
// table. Token counts sum across every observed usage block in the stream
// (covers both top-level usage and item.usage shapes across codex CLI
// versions). Unknown model leaves CostUSD at zero; tokens still recorded.
func ExtractCost(events []coreexec.Event, model string, now time.Time) cost.Capture {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	capture := cost.Capture{
		Adapter:    string(agents.AgentCodex),
		Model:      model,
		CapturedAt: now.UTC(),
	}

	for _, e := range events {
		if e.Type != coreexec.EventStdout {
			continue
		}
		var ev codexUsageEvent
		if err := json.Unmarshal([]byte(e.Data), &ev); err != nil {
			continue
		}
		if ev.Usage != nil {
			capture.InputTokens += ev.Usage.InputTokens
			capture.OutputTokens += ev.Usage.OutputTokens
		}
		if ev.Item != nil && ev.Item.Usage != nil {
			capture.InputTokens += ev.Item.Usage.InputTokens
			capture.OutputTokens += ev.Item.Usage.OutputTokens
		}
	}

	if priced, ok := cost.Compute(string(agents.AgentCodex), model, capture.InputTokens, capture.OutputTokens); ok {
		capture.CostUSD = priced
	}
	return capture
}
