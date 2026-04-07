package config_test

import (
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
