package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/config"
)

// --- AgentPriority field parsing ---

func TestLoadParsesAgentPriority(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"
agent_priority = ["claude", "codex", "gemini"]
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	want := []string{"claude", "codex", "gemini"}
	got := loaded.Config.Project.AgentPriority
	if len(got) != len(want) {
		t.Fatalf("agent_priority length: want %d, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("agent_priority[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestLoadMissingPriorityDefaultsToNil(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Config.Project.AgentPriority != nil {
		t.Fatalf("expected nil agent_priority, got %v", loaded.Config.Project.AgentPriority)
	}
}

// --- EffectivePriority ---

func TestEffectivePriorityFallsBackToDefaultAgent(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{DefaultAgent: "claude"},
	}

	got := cfg.EffectivePriority()
	if len(got) != 1 || got[0] != "claude" {
		t.Fatalf("want [claude], got %v", got)
	}
}

func TestEffectivePriorityReturnsExplicitList(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{
			DefaultAgent:  "claude",
			AgentPriority: []string{"codex", "gemini"},
		},
	}

	got := cfg.EffectivePriority()
	want := []string{"codex", "gemini"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

// --- Save ---

func TestSaveWritesAgentPriority(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	loaded.Config.Project.AgentPriority = []string{"claude", "codex", "gemini"}
	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	got := reloaded.Config.Project.AgentPriority
	want := []string{"claude", "codex", "gemini"}
	if len(got) != len(want) {
		t.Fatalf("agent_priority length: want %d, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("agent_priority[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestSaveSyncsDefaultAgentFromPriority(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	loaded.Config.Project.AgentPriority = []string{"codex", "claude", "gemini"}
	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if reloaded.Config.Project.DefaultAgent != "codex" {
		t.Fatalf("default_agent should sync to priority[0]: want codex, got %q",
			reloaded.Config.Project.DefaultAgent)
	}
}

func TestSavePreservesPlanOverrides(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"

[plans.release]
agent = "codex"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	loaded.Config.Project.AgentPriority = []string{"claude", "gemini"}
	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := reloaded.Config.AgentForPlan("release"); got != "codex" {
		t.Fatalf("plan override lost: want codex, got %q", got)
	}
}

func TestSaveRoundTripsGeminiExecutionConfig(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "gemini"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	loaded.Config.Agents.Gemini.ApprovalMode = "yolo"
	loaded.Config.Agents.Gemini.SandboxMode = "sandbox-exec"
	loaded.Config.Agents.Gemini.Model = "pro"
	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Config.Agents.Gemini.ApprovalMode; got != "yolo" {
		t.Fatalf("approval_mode: got %q", got)
	}
	if got := reloaded.Config.Agents.Gemini.SandboxMode; got != "sandbox-exec" {
		t.Fatalf("sandbox_mode: got %q", got)
	}
	if got := reloaded.Config.Agents.Gemini.Model; got != "pro" {
		t.Fatalf("model: got %q", got)
	}
}

func TestSaveRoundTripsAgentExecutionConfig(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	loaded.Config.Agents.Claude.PermissionMode = "bypassPermissions"
	loaded.Config.Agents.Codex.SandboxMode = "danger-full-access"
	loaded.Config.Agents.Codex.ApprovalPolicy = "never"
	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := reloaded.Config.Agents.Claude.PermissionMode; got != "bypassPermissions" {
		t.Fatalf("claude permission_mode: want bypassPermissions, got %q", got)
	}
	if got := reloaded.Config.Agents.Codex.SandboxMode; got != "danger-full-access" {
		t.Fatalf("codex sandbox_mode: want danger-full-access, got %q", got)
	}
	if got := reloaded.Config.Agents.Codex.ApprovalPolicy; got != "never" {
		t.Fatalf("codex approval_policy: want never, got %q", got)
	}
}

func TestSaveOmitsEmptyAgentExecutionConfigBlocks(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}

	text := string(data)
	for _, unwanted := range []string{"[agents]", "[agents.claude]", "[agents.codex]"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("expected saved config to omit %q, got:\n%s", unwanted, text)
		}
	}
}

func TestSavePreservesOffExecutionModesCleanly(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	loaded.Config.ApplyExecutionMode("claude", config.ExecutionModeOff)
	loaded.Config.ApplyExecutionMode("codex", config.ExecutionModeOff)
	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := reloaded.Config.ExecutionModes().Claude; got != config.ExecutionModeOff {
		t.Fatalf("claude mode: want %q, got %q", config.ExecutionModeOff, got)
	}
	if got := reloaded.Config.ExecutionModes().Codex; got != config.ExecutionModeOff {
		t.Fatalf("codex mode: want %q, got %q", config.ExecutionModeOff, got)
	}
}

func TestSaveRoundTripsStartKeepAwakeFalse(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"

[start]
keep_awake = false
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Config.KeepAwakeEnabled() {
		t.Fatal("keep_awake = false lost after Save round-trip")
	}
}

func TestInitMergePreservesStartKeepAwakeFalse(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"

[start]
keep_awake = false
`)
	if err := os.MkdirAll(filepath.Join(root, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := config.Init(root, []string{"claude"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	reloaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Config.KeepAwakeEnabled() {
		t.Fatal("keep_awake = false lost after Init merge")
	}
}

func TestSaveRoundTripsExplicitOffExecutionConfig(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	loaded.Config.ApplyExecutionMode("claude", config.ExecutionModeOff)
	loaded.Config.ApplyExecutionMode("codex", config.ExecutionModeOff)
	if err := config.Save(loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}

	text := string(data)
	for _, wanted := range []string{
		"[agents]",
		"[agents.claude]",
		"[agents.codex]",
	} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("expected saved config to contain %q, got:\n%s", wanted, text)
		}
	}
}
