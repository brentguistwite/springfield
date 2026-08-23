package agents_test

import (
	"testing"

	"springfield/internal/core/agents"
)

// TestSupportedForExecutionOrdering pins the lead-agent rule governed by the
// ClaudeHeadlessMetered master switch. The set is fixed; only the lead (= the
// init picker's default and the default agent_priority head) moves.
func TestSupportedForExecutionOrdering(t *testing.T) {
	// Shipped default: Anthropic reverted the 2026-05-14 `claude -p` metering
	// change, so claude is once again subscription-friendly and leads.
	if agents.ClaudeHeadlessMetered {
		t.Fatalf("ClaudeHeadlessMetered must default to false (claude leads when not separately metered)")
	}

	cases := []struct {
		name    string
		metered bool
		want    []agents.ID
	}{
		{"not-metered default: claude leads", false, []agents.ID{agents.AgentClaude, agents.AgentCodex, agents.AgentGemini, agents.AgentOpenCode}},
		{"metered: codex leads", true, []agents.ID{agents.AgentCodex, agents.AgentClaude, agents.AgentGemini, agents.AgentOpenCode}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := agents.ClaudeHeadlessMetered
			agents.ClaudeHeadlessMetered = tc.metered
			t.Cleanup(func() { agents.ClaudeHeadlessMetered = prev })

			result := agents.SupportedForExecution()
			if len(result) != len(tc.want) {
				t.Fatalf("expected %d execution-supported agents, got %d: %v", len(tc.want), len(result), result)
			}
			for i, want := range tc.want {
				if result[i] != want {
					t.Fatalf("position %d: got %q, want %q (full: %v)", i, result[i], want, result)
				}
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
		{agents.AgentOpenCode, true},
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
