package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"springfield/internal/core/agents"
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

func TestResolveModelsPromptsInteractivelyEvenWithAgentsFlag(t *testing.T) {
	in := strings.NewReader("claude-sonnet-4-6\n\n")
	var out bytes.Buffer

	models, err := resolveModels(
		"claude,codex",
		"",
		true,
		[]string{"claude", "codex"},
		in,
		&out,
		func(id agents.ID) []string {
			switch id {
			case agents.AgentClaude:
				return []string{"claude-sonnet-4-6"}
			case agents.AgentCodex:
				return []string{"gpt-5-codex"}
			default:
				return nil
			}
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := models["claude"]; got != "claude-sonnet-4-6" {
		t.Fatalf("claude model = %q, want claude-sonnet-4-6", got)
	}
	if _, ok := models["codex"]; ok {
		t.Fatalf("expected blank codex input to omit model, got %v", models)
	}
	if !strings.Contains(out.String(), "Model for claude") || !strings.Contains(out.String(), "Model for codex") {
		t.Fatalf("expected prompt output for both agents, got:\n%s", out.String())
	}
}

func TestParseAndValidateModelsRejectsNoUsableEntries(t *testing.T) {
	_, err := parseAndValidateModels(" , ", []string{"claude"})
	if err == nil {
		t.Fatal("expected error for empty --model value")
	}
	if !strings.Contains(err.Error(), "at least one agent=model entry is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewModelSuggesterUnknownAgentPanicsWithImpossibleState(t *testing.T) {
	suggester := newModelSuggesterFromRegistry(agents.NewRegistry())

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("expected panic")
		}
		msg, ok := recovered.(string)
		if !ok {
			t.Fatalf("panic = %T, want string", recovered)
		}
		if !strings.Contains(msg, `impossible state: no adapter registered for agent "bogus"`) {
			t.Fatalf("unexpected panic message: %q", msg)
		}
	}()

	_ = suggester(agents.ID("bogus"))
}

func TestNewModelSuggesterReturnsNilWhenAdapterHasNoModelProvider(t *testing.T) {
	suggester := newModelSuggesterFromRegistry(agents.NewRegistry(fakeAdapterNoModelProvider{id: agents.AgentClaude}))

	if got := suggester(agents.AgentClaude); got != nil {
		t.Fatalf("suggestions = %v, want nil", got)
	}
}

type fakeAdapterNoModelProvider struct {
	id agents.ID
}

func (f fakeAdapterNoModelProvider) ID() agents.ID {
	return f.id
}

func (f fakeAdapterNoModelProvider) Metadata() agents.Metadata {
	return agents.Metadata{ID: f.id}
}

func (f fakeAdapterNoModelProvider) Detect(context.Context) agents.Detection {
	return agents.Detection{ID: f.id}
}
