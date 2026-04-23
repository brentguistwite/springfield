package agents_test

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/gemini"
)

func newGeminiWorkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

func TestGeminiCommandIncludesStreamJsonAndPromptViaStdin(t *testing.T) {
	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	workDir := newGeminiWorkDir(t)

	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hello",
		WorkDir: workDir,
		ExecutionSettings: agents.ExecutionSettings{
			Gemini: agents.GeminiExecutionSettings{
				ApprovalMode: "yolo",
				SandboxMode:  "sandbox-exec",
				Model:        "pro",
			},
		},
	})
	if err != nil {
		t.Fatalf("Command err: %v", err)
	}
	if cmd.Name != "gemini" {
		t.Fatalf("name: want gemini, got %q", cmd.Name)
	}
	for _, arg := range cmd.Args {
		if arg == "-p" || arg == "--prompt" {
			t.Fatalf("args must not contain -p/--prompt (headless auto-triggers on piped stdin): %v", cmd.Args)
		}
	}
	assertArgsContain(t, cmd.Args, "--output-format", "stream-json")
	assertArgsContain(t, cmd.Args, "--approval-mode", "yolo")
	assertArgsContain(t, cmd.Args, "--sandbox", "sandbox-exec")
	assertArgsContain(t, cmd.Args, "--model", "pro")
	if cmd.Stdin != "hello" {
		t.Fatalf("stdin: want hello, got %q", cmd.Stdin)
	}
	if cmd.Dir != workDir {
		t.Fatalf("dir: want %s, got %s", workDir, cmd.Dir)
	}
}

func TestGeminiCommandOmitsUnsetOptions(t *testing.T) {
	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	workDir := newGeminiWorkDir(t)

	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("Command err: %v", err)
	}
	assertArgsDoNotContain(t, cmd.Args, "--approval-mode")
	assertArgsDoNotContain(t, cmd.Args, "--sandbox")
	assertArgsDoNotContain(t, cmd.Args, "--model")
	assertArgsDoNotContain(t, cmd.Args, "-p")
	assertArgsDoNotContain(t, cmd.Args, "--prompt")
	assertArgsContain(t, cmd.Args, "--output-format", "stream-json")
}

func TestGeminiCommandTrimsWhitespaceInExecutionSettings(t *testing.T) {
	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	workDir := newGeminiWorkDir(t)

	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
		ExecutionSettings: agents.ExecutionSettings{
			Gemini: agents.GeminiExecutionSettings{
				ApprovalMode: "  yolo  ",
				SandboxMode:  "  sandbox-exec  ",
				Model:        "  pro  ",
			},
		},
	})
	if err != nil {
		t.Fatalf("Command err: %v", err)
	}
	assertArgsContain(t, cmd.Args, "--approval-mode", "yolo")
	assertArgsContain(t, cmd.Args, "--sandbox", "sandbox-exec")
	assertArgsContain(t, cmd.Args, "--model", "pro")
}
