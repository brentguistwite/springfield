package agents

import "slices"

// executionOrder returns the execution-supported agents in priority order.
// The lead position depends on ClaudeHeadlessMetered; the tail is fixed.
// When claude headless runs are NOT separately metered (the shipped default),
// claude leads as the path of least resistance and matches the catalog order;
// when metered, codex leads so subscription-friendly defaults win. The order
// is also the init picker's display + default-priority order (lead = default).
//
// opencode is appended LAST (2026-08) so it stays opt-in: the init picker
// offers it unchecked and default priority never gains it automatically —
// priority stays user-authored.
func executionOrder() []ID {
	if ClaudeHeadlessMetered {
		return []ID{AgentCodex, AgentClaude, AgentGemini, AgentOpenCode}
	}
	return []ID{AgentClaude, AgentCodex, AgentGemini, AgentOpenCode}
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
