package agents_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/gemini"
)

func TestGeminiCommandSetsSystemSettingsEnvVar(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hello",
		WorkDir: workDir,
		ExecutionSettings: agents.ExecutionSettings{
			Gemini: agents.GeminiExecutionSettings{ApprovalMode: "yolo"},
		},
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	path := cmd.Env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"]
	if path == "" {
		t.Fatalf("expected GEMINI_CLI_SYSTEM_SETTINGS_PATH set, env=%+v", cmd.Env)
	}
	want := filepath.Join(workDir, ".springfield", "gemini-system-settings.json")
	if path != want {
		t.Fatalf("path: want %s, got %s", want, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file missing: %v", err)
	}
}

func TestGeminiSystemSettingsRegistersBeforeToolHook(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	path := cmd.Env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var parsed struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Name    string `json:"name"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	before := parsed.Hooks["BeforeTool"]
	if len(before) != 1 {
		t.Fatalf("want 1 BeforeTool group, got %d", len(before))
	}
	if before[0].Matcher != "write_file|replace|run_shell_command" {
		t.Fatalf("matcher: %s", before[0].Matcher)
	}
	if len(before[0].Hooks) == 0 {
		t.Fatalf("want inner hooks, got none")
	}
	if !strings.Contains(before[0].Hooks[0].Command, "hook-guard") {
		t.Fatalf("command missing hook-guard: %s", before[0].Hooks[0].Command)
	}
	if before[0].Hooks[0].Type != "command" {
		t.Fatalf("type: want command, got %q", before[0].Hooks[0].Type)
	}
}

func TestGeminiSystemSettingsDisablesSpringfieldSkills(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	path := cmd.Env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var parsed struct {
		Agents struct {
			Overrides map[string]struct {
				Enabled bool `json:"enabled"`
			} `json:"overrides"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, name := range []string{"springfield:start", "springfield:plan", "springfield:status", "springfield:recover"} {
		entry, ok := parsed.Agents.Overrides[name]
		if !ok {
			t.Errorf("missing overrides entry for %s", name)
			continue
		}
		if entry.Enabled {
			t.Errorf("%s: enabled should be false, got true", name)
		}
	}
}

func TestGeminiSystemSettingsEmbedsAbsoluteSpringfieldBinary(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	data, err := os.ReadFile(cmd.Env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)

	// The embedded command should be the absolute springfield binary path
	// (from os.Executable()) followed by " hook-guard". We can't hard-code
	// the binary path, but we can assert the suffix and that the leading
	// portion is shell-quoted.
	if !strings.Contains(text, "hook-guard") {
		t.Fatalf("expected hook-guard command, got:\n%s", text)
	}
	// Shell-quoted path starts with a single quote.
	if !strings.Contains(text, "' hook-guard") {
		t.Fatalf("expected shell-quoted binary path followed by hook-guard, got:\n%s", text)
	}
}

func TestGeminiCommandRefusesToSpawnOnSettingsWriteFailure(t *testing.T) {
	// Point WorkDir at a path whose .springfield/ cannot be created:
	// existing file (not dir) at .springfield makes os.WriteFile fail.
	workDir := t.TempDir()
	// Create .springfield as a regular file so WriteFile to
	// .springfield/gemini-system-settings.json errors (not a directory).
	if err := os.WriteFile(filepath.Join(workDir, ".springfield"), []byte("blocker"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
	})
	if err == nil {
		t.Fatalf("expected error, got nil; cmd=%+v", cmd)
	}
	if !strings.Contains(err.Error(), "cannot install control-plane hook") {
		t.Fatalf("unexpected error message: %v", err)
	}
	if cmd.Name != "" || cmd.Dir != "" || len(cmd.Args) != 0 {
		t.Fatalf("expected zero-valued Command on failure, got %+v", cmd)
	}
}

func TestGeminiSystemSettingsFilePermissions(t *testing.T) {
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := gemini.New(func(string) (string, error) { return "/opt/bin/gemini", nil })
	cmd, err := a.(agents.Commander).Command(agents.CommandInput{
		Prompt:  "hi",
		WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	info, err := os.Stat(cmd.Env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"])
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions: want 0600, got %o", info.Mode().Perm())
	}
}
