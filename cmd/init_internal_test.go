package cmd

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"

	"springfield/internal/core/agents"
)

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

// TestPreservedOrderUsesCanonicalAgentOrdering pins the rule that the final
// priority list follows the supported-agents canonical order rather than the
// operator's toggle order. Two operators picking the same set must end up
// with byte-identical springfield.toml.
func TestPreservedOrderUsesCanonicalAgentOrdering(t *testing.T) {
	canonical := []agents.ID{agents.AgentClaude, agents.AgentCodex, agents.AgentGemini}

	cases := []struct {
		name     string
		selected []string
		want     []string
	}{
		{"toggle-reverse", []string{"gemini", "codex", "claude"}, []string{"claude", "codex", "gemini"}},
		{"toggle-codex-first", []string{"codex", "claude"}, []string{"claude", "codex"}},
		{"empty", nil, []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := preservedOrder(tc.selected, canonical)
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d (%v)", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestCollectModelsOmitsBlanks pins the rule that a blank-string selection
// (the "(adapter default)" option) is omitted from the result map so the
// downstream config.Init path leaves the corresponding TOML model line out.
func TestCollectModelsOmitsBlanks(t *testing.T) {
	picked := "claude-sonnet-4-6"
	empty := ""
	whitespace := "   "

	modelTargets := map[string]*string{
		"claude": &picked,
		"codex":  &empty,
		"gemini": &whitespace,
	}

	models := collectModels([]string{"claude", "codex", "gemini"}, modelTargets)

	if models["claude"] != "claude-sonnet-4-6" {
		t.Errorf("claude = %q, want claude-sonnet-4-6", models["claude"])
	}
	if _, ok := models["codex"]; ok {
		t.Errorf("codex should be omitted (adapter default), got %q", models["codex"])
	}
	if _, ok := models["gemini"]; ok {
		t.Errorf("gemini should be omitted (whitespace-only), got %q", models["gemini"])
	}
}

// TestLineByLineReaderEnforcesOneLinePerRead pins the contract that drives
// huh's accessible-mode compatibility: a single Read call must never return
// more than one logical line, even when the producer delivered every byte
// up front. Without this, the first prompt's bufio.Scanner would drain the
// whole answer script and starve every later prompt.
func TestLineByLineReaderEnforcesOneLinePerRead(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    []string
		wantErr error
	}{
		{"lf", "alpha\nbravo\ncharlie\n", []string{"alpha\n", "bravo\n", "charlie\n"}, io.EOF},
		{"crlf", "alpha\r\nbravo\r\n", []string{"alpha\r", "bravo\r"}, io.EOF},
		{"cr-only", "alpha\rbravo\r", []string{"alpha\r", "bravo\r"}, io.EOF},
		{"no-trailing-newline", "alpha\nbravo", []string{"alpha\n", "bravo"}, io.EOF},
		{"empty", "", nil, io.EOF},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lr := &lineByLineReader{r: bufio.NewReader(strings.NewReader(tc.input))}
			buf := make([]byte, 64)
			for _, want := range tc.want {
				n, err := lr.Read(buf)
				if err != nil {
					t.Fatalf("Read returned %v before consuming all lines", err)
				}
				if got := string(buf[:n]); got != want {
					t.Fatalf("got %q, want %q", got, want)
				}
			}
			// Drain to EOF.
			_, err := lr.Read(buf)
			if err != tc.wantErr {
				t.Fatalf("final err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestLineByLineReaderRespectsBufferLimit pins the safety net for the
// pathological case where a single "line" exceeds the caller's buffer:
// the reader must return what it has rather than overflow.
func TestLineByLineReaderRespectsBufferLimit(t *testing.T) {
	long := strings.Repeat("x", 100) + "\n"
	lr := &lineByLineReader{r: bufio.NewReader(strings.NewReader(long))}
	buf := make([]byte, 16)

	n, err := lr.Read(buf)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if n != 16 {
		t.Fatalf("expected to fill buffer, got n=%d", n)
	}
	if string(buf) != strings.Repeat("x", 16) {
		t.Fatalf("buffer content = %q, want 16 x's", string(buf))
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
