package agents_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/opencode"
)

func TestOpenCodeCommandIncludesRunFormatAutoAndPromptViaStdin(t *testing.T) {
	a := opencode.New(func(string) (string, error) { return "/opt/bin/opencode", nil })
	workDir := t.TempDir()

	cmd, err := a.Command(agents.CommandInput{
		Prompt:  "hello",
		WorkDir: workDir,
		ExecutionSettings: agents.ExecutionSettings{
			OpenCode: agents.OpenCodeExecutionSettings{Model: "openai/gpt-5.4"},
		},
	})
	if err != nil {
		t.Fatalf("Command err: %v", err)
	}
	if cmd.Name != "opencode" {
		t.Fatalf("name: want opencode, got %q", cmd.Name)
	}
	assertArgsContain(t, cmd.Args, "run", "")
	assertArgsContain(t, cmd.Args, "--format", "json")
	assertArgsContain(t, cmd.Args, "--auto", "")
	assertArgsContain(t, cmd.Args, "--model", "openai/gpt-5.4")
	for _, forbidden := range []string{"-c", "-s", "--fork", "--continue"} {
		assertArgsDoNotContain(t, cmd.Args, forbidden)
	}
	// Prompt must ride stdin, never argv (256 KB context_md vs ARG_MAX).
	for _, arg := range cmd.Args {
		if arg == "hello" {
			t.Fatalf("prompt leaked into argv: %v", cmd.Args)
		}
	}
	if cmd.Stdin != "hello" {
		t.Fatalf("stdin: want hello, got %q", cmd.Stdin)
	}
	if cmd.Dir != workDir {
		t.Fatalf("dir: want %s, got %s", workDir, cmd.Dir)
	}
}

func TestOpenCodeCommandInjectsControlPlaneConfig(t *testing.T) {
	a := opencode.New(func(string) (string, error) { return "/opt/bin/opencode", nil })
	workDir := t.TempDir()

	cmd, err := a.Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("Command err: %v", err)
	}

	wantDir := filepath.Join(workDir, ".springfield", "opencode")
	if got := cmd.Env["OPENCODE_CONFIG_DIR"]; got != wantDir {
		t.Fatalf("OPENCODE_CONFIG_DIR: want %s, got %q", wantDir, got)
	}
	hookBin := cmd.Env["SPRINGFIELD_HOOK_BIN"]
	if hookBin == "" {
		t.Fatalf("expected SPRINGFIELD_HOOK_BIN set, env=%+v", cmd.Env)
	}

	content := cmd.Env["OPENCODE_CONFIG_CONTENT"]
	if content == "" {
		t.Fatal("expected OPENCODE_CONFIG_CONTENT set")
	}
	var parsed struct {
		Autoupdate bool `json:"autoupdate"`
		Share      any  `json:"share"`
		Permission struct {
			Edit map[string]string `json:"edit"`
		} `json:"permission"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not JSON (%v): %s", err, content)
	}
	if parsed.Autoupdate {
		t.Fatalf("autoupdate must be false, content:\n%s", content)
	}
	if parsed.Share != "disabled" {
		t.Fatalf(`share must be "disabled", got %#v; content:\n%s`, parsed.Share, content)
	}
	for _, pattern := range []string{"**/.springfield/**", ".springfield/**"} {
		if got := parsed.Permission.Edit[pattern]; got != "deny" {
			t.Fatalf("edit rule %q: want deny, got %q; content:\n%s", pattern, got, content)
		}
	}
	if got := parsed.Permission.Edit["*"]; got != "allow" {
		t.Fatalf(`edit rule "*": want allow, got %q; content:\n%s`, got, content)
	}
}

func TestOpenCodeWritesGuardPluginIdempotently(t *testing.T) {
	a := opencode.New(func(string) (string, error) { return "/opt/bin/opencode", nil })
	workDir := t.TempDir()
	input := agents.CommandInput{Prompt: "hi", WorkDir: workDir}

	if _, err := a.Command(input); err != nil {
		t.Fatalf("Command err: %v", err)
	}
	path := filepath.Join(workDir, ".springfield", "opencode", "plugins", "springfield-guard.js")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read guard plugin after first Command: %v", err)
	}
	// The plugin must shell out to the guard binary with --block-reentry
	// (argv-array form; see cmd/hook_guard.go).
	if !strings.Contains(string(first), `"hook-guard"`) || !strings.Contains(string(first), `"--block-reentry"`) {
		t.Fatalf("guard plugin missing hook-guard --block-reentry invocation:\n%s", first)
	}
	if !strings.Contains(string(first), "tool.execute.before") {
		t.Fatalf("guard plugin missing tool.execute.before hook:\n%s", first)
	}

	// Idempotent bytes on rewrite.
	if _, err := a.Command(input); err != nil {
		t.Fatalf("second Command err: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read guard plugin after second Command: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("guard plugin bytes changed on rewrite")
	}
}

func TestOpenCodeCommandRefusesToSpawnOnGuardWriteFailure(t *testing.T) {
	// Point WorkDir at a path whose .springfield/ cannot be created: an
	// existing regular file at .springfield makes the guard dir MkdirAll fail.
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, ".springfield"), []byte("blocker"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	a := opencode.New(func(string) (string, error) { return "/opt/bin/opencode", nil })
	cmd, err := a.Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
	})
	if err == nil {
		t.Fatalf("expected error, got nil; cmd=%+v", cmd)
	}
	if !strings.Contains(err.Error(), "cannot install control-plane hook") ||
		!strings.Contains(err.Error(), "refusing to spawn") {
		t.Fatalf("unexpected error message: %v", err)
	}
	if cmd.Name != "" || cmd.Dir != "" || len(cmd.Args) != 0 {
		t.Fatalf("expected zero-valued Command on failure, got %+v", cmd)
	}
}
