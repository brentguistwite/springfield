package agents

import "slices"

// supportedForExecution lists agents with fully wired execution support.
var supportedForExecution = []ID{AgentClaude, AgentCodex, AgentGemini}

// defaultInitPriority is intentionally narrower than supportedForExecution:
// Gemini is execution-supported but is NOT auto-included in fresh-init
// priority. Users opt in via explicit --agents claude,codex,gemini, per the
// agent-priority-stays-user-authored rule in the roadmap plan.
var defaultInitPriority = []ID{AgentClaude, AgentCodex}

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

// DefaultInitPriority returns the agent priority list used when the user
// does not pass --agents on springfield init. Intentionally narrower than
// SupportedForExecution — Gemini is execution-supported but opt-in only.
func DefaultInitPriority() []ID {
	out := make([]ID, len(defaultInitPriority))
	copy(out, defaultInitPriority)
	return out
}
