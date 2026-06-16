package agents

import "slices"

// executionOrder returns the execution-supported agents in priority order.
// The set is fixed; only the lead position depends on ClaudeHeadlessMetered.
// When claude headless runs are NOT separately metered (the shipped default),
// claude leads as the path of least resistance and matches the catalog order;
// when metered, codex leads so subscription-friendly defaults win. The order
// is also the init picker's display + default-priority order (lead = default).
func executionOrder() []ID {
	if ClaudeHeadlessMetered {
		return []ID{AgentCodex, AgentClaude, AgentGemini}
	}
	return []ID{AgentClaude, AgentCodex, AgentGemini}
}

// SupportedForExecution returns the ordered list of agent IDs that can be
// used for plan execution. This is the single source of truth; use it
// anywhere execution eligibility must be checked.
func SupportedForExecution() []ID {
	return executionOrder()
}

// IsExecutionSupported reports whether the given agent ID is wired for
// execution.
func IsExecutionSupported(id ID) bool {
	return slices.Contains(executionOrder(), id)
}
