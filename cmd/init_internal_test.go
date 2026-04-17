package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestResolvePriorityInteractiveEmptyInput verifies empty input returns the default list.
func TestResolvePriorityInteractiveEmptyInput(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer

	got, err := resolvePriority("", true, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := defaultPriority()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i], want[i])
		}
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
	// "gemini" is not supported → triggers re-prompt; "claude,codex" succeeds.
	in := strings.NewReader("gemini\nclaude,codex\n")
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

	// Confirm the error message was printed during the retry.
	if !strings.Contains(out.String(), "gemini is not yet supported") {
		t.Errorf("expected rejection message in output, got: %q", out.String())
	}
}
