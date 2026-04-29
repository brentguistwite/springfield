package agents_test

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	coreexec "springfield/internal/core/exec"
)

// Cross-adapter matrix exercising the positive-signal contract for
// ValidateResult. Each row pairs a fixture (newline-delimited stdout JSON
// events) with the expected outcome (nil vs non-nil error).
//
// Policy A (strict) for Codex and Gemini: text-only runs are failures. A run
// is success only if the transcript contains an explicit positive completion
// signal (Claude/Gemini: at least one tool_use paired with tool_result
// is_error=false; Codex: at least one item.completed whose item.type is a
// real tool/function-call, not agent_message/reasoning).
func TestValidateMatrix(t *testing.T) {
	type row struct {
		name        string
		fixture     string
		exitCode    int
		stderrLines []string
		expectNil   bool
	}

	type adapterCase struct {
		name      string
		validator agents.ResultValidator
		dir       string
		rows      []row
	}

	claudeAdapter := claude.New(exec.LookPath)
	codexAdapter := codex.New(exec.LookPath)
	geminiAdapter := gemini.New(exec.LookPath)

	cases := []adapterCase{
		{
			name:      "claude",
			validator: mustValidator(t, claudeAdapter),
			dir:       "fixtures/claude",
			rows: []row{
				{name: "success", fixture: "success.json", expectNil: true},
				{name: "refusal-no-tools", fixture: "refusal-no-tools.json", expectNil: false},
				{name: "tool-error-partial", fixture: "tool-error-partial.json", expectNil: true},
				{name: "tool-error-all", fixture: "tool-error-all.json", expectNil: false},
				{name: "hard-error", fixture: "hard-error.json", exitCode: 1, expectNil: false},
			},
		},
		{
			name:      "codex",
			validator: mustValidator(t, codexAdapter),
			dir:       "fixtures/codex",
			rows: []row{
				{name: "success", fixture: "success.json", expectNil: true},
				{name: "success-text-only", fixture: "success-text-only.json", expectNil: false},
				{name: "refusal-no-tools", fixture: "refusal-no-tools.json", expectNil: false},
				{name: "tool-error-partial", fixture: "tool-error-partial.json", expectNil: true},
				{name: "tool-error-all", fixture: "tool-error-all.json", expectNil: false},
				{
					name:    "hard-error",
					fixture: "hard-error.json",
					stderrLines: []string{
						`2026-04-08T13:57:19Z ERROR rmcp::transport::worker: worker quit with fatal: Transport channel closed`,
					},
					expectNil: false,
				},
			},
		},
		{
			name:      "gemini",
			validator: mustValidator(t, geminiAdapter),
			dir:       "fixtures/gemini",
			rows: []row{
				{name: "success", fixture: "success.json", expectNil: true},
				{name: "success-text-only", fixture: "success-text-only.json", expectNil: false},
				{name: "refusal-no-tools", fixture: "refusal-no-tools.json", expectNil: false},
				{name: "tool-error-partial", fixture: "tool-error-partial.json", expectNil: true},
				{name: "tool-error-all", fixture: "tool-error-all.json", expectNil: false},
				{name: "hard-error", fixture: "hard-error.json", exitCode: 1, expectNil: false},
				{name: "exit-53-turn-limit", fixture: "exit-53-turn-limit.json", exitCode: 53, expectNil: false},
				{name: "exit-42-rejected-input", fixture: "exit-42-rejected-input.json", exitCode: 42, expectNil: false},
			},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			for _, r := range c.rows {
				r := r
				t.Run(r.name, func(t *testing.T) {
					events := loadFixtureEvents(t, filepath.Join(c.dir, r.fixture))
					for _, line := range r.stderrLines {
						events = append(events, coreexec.Event{Type: coreexec.EventStderr, Data: line})
					}
					result := coreexec.Result{ExitCode: r.exitCode, Events: events}
					err := c.validator.ValidateResult(result)
					if r.expectNil && err != nil {
						t.Fatalf("%s/%s: expected nil, got %v", c.name, r.name, err)
					}
					if !r.expectNil {
						if err == nil {
							t.Fatalf("%s/%s: expected non-nil error", c.name, r.name)
						}
						if strings.TrimSpace(err.Error()) == "" {
							t.Fatalf("%s/%s: error must have non-empty message", c.name, r.name)
						}
					}
				})
			}
		})
	}
}

func mustValidator(t *testing.T, a agents.Adapter) agents.ResultValidator {
	t.Helper()
	v, ok := a.(agents.ResultValidator)
	if !ok {
		t.Fatalf("adapter %T does not implement ResultValidator", a)
	}
	return v
}

func loadFixtureEvents(t *testing.T, path string) []coreexec.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", path, err)
	}
	defer f.Close()

	var events []coreexec.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		events = append(events, coreexec.Event{Type: coreexec.EventStdout, Data: line})
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture %s: %v", path, err)
	}
	return events
}
