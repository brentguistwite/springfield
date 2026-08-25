package planner

import (
	"testing"

	"springfield/internal/core/agents"
)

// TestMissingConfigFallbackPriorityIncludesAllAdapters pins the error-path
// agent_priority used when no springfield.toml exists. Every registered
// execution adapter belongs in the fallback chain — omitting one (opencode
// was historically missing while opt-in gemini was present) silently narrows
// the fallback surface for unconfigured projects.
func TestMissingConfigFallbackPriorityIncludesAllAdapters(t *testing.T) {
	want := []agents.ID{
		agents.AgentClaude,
		agents.AgentCodex,
		agents.AgentGemini,
		agents.AgentOpenCode,
	}

	got := missingConfigFallbackPriority()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
