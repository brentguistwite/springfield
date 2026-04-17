package agents

import "slices"

// supportedForExecution lists agents with fully wired execution support.
// Gemini is intentionally excluded — it is detectable (doctor lists it)
// but has no ExecutionSettings or Command implementation yet.
var supportedForExecution = []ID{AgentClaude, AgentCodex}

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
