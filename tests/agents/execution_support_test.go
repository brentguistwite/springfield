package agents_test

import (
	"testing"

	"springfield/internal/core/agents"
)

func TestSupportedForExecutionReturnsSupportedIDs(t *testing.T) {
	cases := []struct {
		pos int
		id  agents.ID
	}{
		{0, agents.AgentClaude},
		{1, agents.AgentCodex},
		{2, agents.AgentGemini},
	}

	result := agents.SupportedForExecution()

	if len(result) != len(cases) {
		t.Fatalf("expected %d execution-supported agents, got %d: %v", len(cases), len(result), result)
	}

	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			if result[tc.pos] != tc.id {
				t.Fatalf("expected result[%d]=%q, got %q", tc.pos, tc.id, result[tc.pos])
			}
		})
	}
}

func TestIsExecutionSupported(t *testing.T) {
	cases := []struct {
		id   agents.ID
		want bool
	}{
		{agents.AgentClaude, true},
		{agents.AgentCodex, true},
		{agents.AgentGemini, true},
		{agents.ID("unknown"), false},
	}

	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			got := agents.IsExecutionSupported(tc.id)
			if got != tc.want {
				t.Fatalf("IsExecutionSupported(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

// TestDefaultInitPriorityExcludesGemini locks the roadmap rule that Gemini
// is execution-supported but opt-in only. Fresh init without --agents must
// not silently insert gemini.
func TestDefaultInitPriorityExcludesGemini(t *testing.T) {
	priority := agents.DefaultInitPriority()
	if len(priority) != 2 {
		t.Fatalf("expected 2 default agents, got %d: %v", len(priority), priority)
	}
	if priority[0] != agents.AgentClaude || priority[1] != agents.AgentCodex {
		t.Fatalf("default priority: want [claude codex], got %v", priority)
	}
}
