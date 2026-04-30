package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestResolvePriorityInteractiveEmptyInput verifies empty input is rejected —
// the picker requires the user to explicitly opt in to at least one agent.
func TestResolvePriorityInteractiveEmptyInput(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer

	_, err := resolvePriority("", true, in, &out)
	if err == nil {
		t.Fatal("expected error on empty input, got nil")
	}
	if !strings.Contains(out.String(), "at least one agent is required") {
		t.Errorf("expected rejection message in output, got: %q", out.String())
	}
}

// TestResolvePriorityInteractiveValidInput verifies a valid comma-separated input is returned.
func TestResolvePriorityInteractiveValidInput(t *testing.T) {
	in := strings.NewReader("codex,claude\n")
	var out bytes.Buffer

	got, err := resolvePriority("", true, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"codex", "claude"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

// TestResolvePriorityInteractiveRetryOnInvalid verifies invalid input re-prompts and
// a subsequent valid entry is accepted.
func TestResolvePriorityInteractiveRetryOnInvalid(t *testing.T) {
	// "unknown" is not a supported agent → triggers re-prompt;
	// "claude,codex" succeeds.
	in := strings.NewReader("unknown\nclaude,codex\n")
	var out bytes.Buffer

	got, err := resolvePriority("", true, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"claude", "codex"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}

	// Confirm a rejection message was printed during the retry.
	if !strings.Contains(out.String(), "not") {
		t.Errorf("expected rejection message in output, got: %q", out.String())
	}
}

// TestResolvePriorityInteractiveWhitespaceInput verifies that a line containing
// only whitespace is rejected like empty input — TrimSpace collapses it and
// the picker still requires an explicit agent selection.
func TestResolvePriorityInteractiveWhitespaceInput(t *testing.T) {
	in := strings.NewReader("   \n")
	var out bytes.Buffer

	_, err := resolvePriority("", true, in, &out)
	if err == nil {
		t.Fatal("expected error on whitespace-only input, got nil")
	}
	if !strings.Contains(out.String(), "at least one agent is required") {
		t.Errorf("expected rejection message in output, got: %q", out.String())
	}
}

// TestParseAndValidateAgentsRejectsDuplicates verifies that a duplicate agent ID
// in the priority list is rejected — agent_priority must be a strict ordering.
func TestParseAndValidateAgentsRejectsDuplicates(t *testing.T) {
	_, err := parseAndValidateAgents("claude,claude")
	if err == nil {
		t.Fatal("expected error for duplicate agent, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate error, got: %v", err)
	}
}
