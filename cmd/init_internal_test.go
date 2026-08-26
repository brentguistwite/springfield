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
	claudeFirst := []agents.ID{agents.AgentClaude, agents.AgentCodex, agents.AgentGemini}
	codexFirst := []agents.ID{agents.AgentCodex, agents.AgentClaude, agents.AgentGemini}
	// Canonical order since 2026-08: opencode appended last (opt-in) — its
	// tail position must survive preservation regardless of toggle order.
	opencodeTail := []agents.ID{agents.AgentClaude, agents.AgentCodex, agents.AgentGemini, agents.AgentOpenCode}

	cases := []struct {
		name      string
		canonical []agents.ID
		selected  []string
		want      []string
	}{
		{"toggle-reverse", claudeFirst, []string{"gemini", "codex", "claude"}, []string{"claude", "codex", "gemini"}},
		{"toggle-codex-first", claudeFirst, []string{"codex", "claude"}, []string{"claude", "codex"}},
		{"empty", claudeFirst, nil, []string{}},
		// Metered canonical (ClaudeHeadlessMetered=true): the result follows
		// the codex-first canonical, not the operator's toggle order.
		{"metered-canonical", codexFirst, []string{"claude", "codex"}, []string{"codex", "claude"}},
		{"opencode-toggle-first-still-tail", opencodeTail, []string{"opencode", "claude", "codex", "gemini"}, []string{"claude", "codex", "gemini", "opencode"}},
		{"opencode-solo-selection", opencodeTail, []string{"opencode"}, []string{"opencode"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := preservedOrder(tc.selected, tc.canonical)
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

// TestLineByLineReaderExhaustionHookFires verifies the EOF-counter bail
// path: after maxLineReaderEOFs consecutive zero-byte reads on an empty
// underlying reader, the configured onExhaust hook fires exactly once.
// Production code passes os.Exit; tests pass a recording hook so the
// process is not killed.
func TestLineByLineReaderExhaustionHookFires(t *testing.T) {
	var calls int
	lr := &lineByLineReader{
		r:         bufio.NewReader(strings.NewReader("")),
		onExhaust: func() { calls++ },
	}
	buf := make([]byte, 4)
	for i := 0; i < maxLineReaderEOFs+5; i++ {
		_, err := lr.Read(buf)
		if err != io.EOF {
			t.Fatalf("iter %d: err = %v, want io.EOF", i, err)
		}
	}
	if calls < 1 {
		t.Fatalf("expected exhaustion hook to fire at least once after %d EOFs, got %d", maxLineReaderEOFs+5, calls)
	}
}

// TestLineByLineReaderExhaustionResetsOnData verifies the EOF counter is
// cleared when a real byte arrives mid-form, so a temporarily slow pipe
// (consumer waits for prompt; producer responds) does not trigger the
// exhaustion bail on the cumulative EOFs across prompts.
func TestLineByLineReaderExhaustionResetsOnData(t *testing.T) {
	type chunk struct {
		emit string
		eof  bool
	}
	chunks := []chunk{
		{eof: true}, {eof: true}, {eof: true}, // slow pipe; no data yet
		{emit: "answer\n"}, // producer writes
		{eof: true},
	}
	idx := 0
	var calls int
	pr, pw := io.Pipe()
	go func() {
		for _, c := range chunks {
			if c.emit != "" {
				_, _ = pw.Write([]byte(c.emit))
			}
			idx++
		}
		_ = pw.Close()
	}()
	lr := &lineByLineReader{r: bufio.NewReader(pr), onExhaust: func() { calls++ }}

	// Read the single line. Some intermediate reads will return EOF/0
	// bytes but onExhaust must NOT fire because real data arrives before
	// the EOF count exceeds the threshold.
	buf := make([]byte, 32)
	var got string
	for attempts := 0; attempts < 8 && got == ""; attempts++ {
		n, _ := lr.Read(buf)
		if n > 0 {
			got = string(buf[:n])
		}
	}
	if got != "answer\n" {
		t.Fatalf("got %q, want %q", got, "answer\n")
	}
	if calls != 0 {
		t.Fatalf("exhaustion hook fired %d times during slow-pipe read; should not have fired", calls)
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

// TestRunInitFormPreSelectsLeadAgent pins that the agent picker pre-checks the
// lead agent from agents.SupportedForExecution() — and that the lead tracks the
// ClaudeHeadlessMetered switch. It drives the accessible-mode form in-process
// and confirms the pre-checked default as-is, asserting the resulting priority.
// The subprocess test in tests/cmd cannot flip the in-process switch, so the
// metered (codex-led) path is only reachable here; this also guards against a
// future re-hardcode of the lead in init_form.go on either switch state.
func TestRunInitFormPreSelectsLeadAgent(t *testing.T) {
	cases := []struct {
		name     string
		metered  bool
		wantLead string
	}{
		{"not-metered: claude leads", false, "claude"},
		{"metered: codex leads", true, "codex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := agents.ClaudeHeadlessMetered
			agents.ClaudeHeadlessMetered = tc.metered
			t.Cleanup(func() { agents.ClaudeHeadlessMetered = prev })

			// Accessible-mode answer script: confirm the pre-checked lead as-is
			// (0), take the adapter-default model (1), write (y). Same derivation
			// as TestInitNonTTYPipedAccessibleModeMatchesFlagOutput in tests/cmd.
			in := strings.NewReader("0\n1\ny\n")
			suggest := func(agents.ID) []string { return nil }
			priority, _, err := runInitForm(in, io.Discard, fakeDetector{}, suggest, true)
			if err != nil {
				t.Fatalf("runInitForm: %v", err)
			}
			if len(priority) != 1 || priority[0] != tc.wantLead {
				t.Fatalf("priority = %v, want [%s] (the pre-selected lead)", priority, tc.wantLead)
			}
		})
	}
}

// fakeDetector reports every agent as available so the picker renders all rows
// without a real PATH sweep.
type fakeDetector struct{}

func (fakeDetector) Detect(agents.ID) agents.DetectionStatus {
	return agents.DetectionStatusAvailable
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
