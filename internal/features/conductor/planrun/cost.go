package planrun

import (
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	coreexec "springfield/internal/core/exec"
	"springfield/internal/features/cost"
)

// extractCost dispatches per-iteration cost extraction to the adapter
// whose ID matches the result. Gemini is intentionally unimplemented per
// the cost-visibility decision: its CLI does not expose a usable token
// usage surface, so we record a zero-value Capture tagged with the gemini
// adapter so surfacers can show "$0.00*" with an asterisk rather than
// silently dropping the iteration.
func extractCost(agentID agents.ID, events []coreexec.Event, model string, now time.Time) cost.Capture {
	switch agentID {
	case agents.AgentClaude:
		return claude.ExtractCost(events, model, now)
	case agents.AgentCodex:
		return codex.ExtractCost(events, model, now)
	default:
		if now.IsZero() {
			now = time.Now().UTC()
		}
		return cost.Capture{
			Adapter:    string(agentID),
			Model:      model,
			CapturedAt: now.UTC(),
		}
	}
}
