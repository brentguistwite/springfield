package agents

import "slices"

// supportedForExecution lists agents with fully wired execution support.
//
// Order is also the init picker's display + default-priority order. As of
// the 2026-05-14 Anthropic billing change to `claude -p`, codex is listed
// first so subscription-friendly defaults are the path of least resistance;
// claude remains fully supported but is opt-in.
var supportedForExecution = []ID{AgentCodex, AgentClaude, AgentGemini}

// SupportedForExecution returns the ordered list of agent IDs that can be
// used for plan execution. This is the single source of truth; use it
// anywhere execution eligibility must be checked.
func SupportedForExecution() []ID {
	out := make([]ID, len(supportedForExecution))
	copy(out, supportedForExecution)
	return out
}

// IsExecutionSupported reports whether the given agent ID is wired for
// execution.
func IsExecutionSupported(id ID) bool {
	return slices.Contains(supportedForExecution, id)
}
