package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
)

func writeConfigFile(t *testing.T, root string, body string) string {
	t.Helper()

	path := filepath.Join(root, config.FileName)
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	return path
}

func TestLoadFromReadsRepoRootConfig(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "plans", "alpha")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	configPath := writeConfigFile(t, root, `
[project]
default_agent = "claude"

[plans.release]
agent = "codex"
`)

	loaded, err := config.LoadFrom(nested)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if loaded.RootDir != root {
		t.Fatalf("expected root dir %q, got %q", root, loaded.RootDir)
	}

	if loaded.Path != configPath {
		t.Fatalf("expected config path %q, got %q", configPath, loaded.Path)
	}

	if loaded.Config.Project.DefaultAgent != "claude" {
		t.Fatalf("expected default agent claude, got %q", loaded.Config.Project.DefaultAgent)
	}

	if got := loaded.Config.AgentForPlan("release"); got != "codex" {
		t.Fatalf("expected plan override codex, got %q", got)
	}

	if got := loaded.Config.AgentForPlan("missing"); got != "claude" {
		t.Fatalf("expected fallback agent claude, got %q", got)
	}
}

func TestLoadFromReturnsActionableMissingConfigError(t *testing.T) {
	root := t.TempDir()

	_, err := config.LoadFrom(root)
	if err == nil {
		t.Fatal("expected missing config error")
	}

	var missingErr *config.MissingConfigError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected MissingConfigError, got %T", err)
	}

	if !strings.Contains(err.Error(), config.FileName) {
		t.Fatalf("expected error to mention %s, got %q", config.FileName, err.Error())
	}

	if !strings.Contains(err.Error(), "springfield init") {
		t.Fatalf("expected error to mention init guidance, got %q", err.Error())
	}
}

func TestLoadFromRejectsInvalidConfig(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = ""
`)

	_, err := config.LoadFrom(root)
	if err == nil {
		t.Fatal("expected invalid config error")
	}

	var invalidErr *config.InvalidConfigError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("expected InvalidConfigError, got %T", err)
	}

	if !strings.Contains(err.Error(), "project.default_agent") {
		t.Fatalf("expected error to mention project.default_agent, got %q", err.Error())
	}

	if !strings.Contains(err.Error(), filepath.Join(root, config.FileName)) {
		t.Fatalf("expected error to mention config path, got %q", err.Error())
	}
}

func TestLoadParsesClaudeExecutionConfig(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"

[agents.claude]
permission_mode = "bypassPermissions"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := loaded.Config.Agents.Claude.PermissionMode; got != "bypassPermissions" {
		t.Fatalf("expected claude permission_mode bypassPermissions, got %q", got)
	}

	settings := loaded.Config.ExecutionSettingsForAgent(string(agents.AgentClaude))
	if got := settings.Claude.PermissionMode; got != "bypassPermissions" {
		t.Fatalf("expected resolved claude permission_mode bypassPermissions, got %q", got)
	}
}

func TestLoadParsesCodexExecutionConfig(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "codex"

[agents.codex]
sandbox_mode = "workspace-write"
approval_policy = "on-request"
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := loaded.Config.Agents.Codex.SandboxMode; got != "workspace-write" {
		t.Fatalf("expected codex sandbox_mode workspace-write, got %q", got)
	}

	if got := loaded.Config.Agents.Codex.ApprovalPolicy; got != "on-request" {
		t.Fatalf("expected codex approval_policy on-request, got %q", got)
	}

	settings := loaded.Config.ExecutionSettingsForAgent(string(agents.AgentCodex))
	if got := settings.Codex.SandboxMode; got != "workspace-write" {
		t.Fatalf("expected resolved codex sandbox_mode workspace-write, got %q", got)
	}
	if got := settings.Codex.ApprovalPolicy; got != "on-request" {
		t.Fatalf("expected resolved codex approval_policy on-request, got %q", got)
	}
}

func TestLoadRejectsUnknownClaudePermissionMode(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"

[agents.claude]
permission_mode = "invalid"
`)

	_, err := config.LoadFrom(root)
	if err == nil {
		t.Fatal("expected invalid config error")
	}

	if !strings.Contains(err.Error(), "agents.claude.permission_mode must be one of") {
		t.Fatalf("expected actionable claude permission_mode error, got %q", err.Error())
	}
}

func TestLoadRejectsUnknownCodexSandboxMode(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "codex"

[agents.codex]
sandbox_mode = "invalid"
`)

	_, err := config.LoadFrom(root)
	if err == nil {
		t.Fatal("expected invalid config error")
	}

	if !strings.Contains(err.Error(), "agents.codex.sandbox_mode must be one of") {
		t.Fatalf("expected actionable codex sandbox_mode error, got %q", err.Error())
	}
}

func TestLoadRejectsUnknownCodexApprovalPolicy(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "codex"

[agents.codex]
approval_policy = "invalid"
`)

	_, err := config.LoadFrom(root)
	if err == nil {
		t.Fatal("expected invalid config error")
	}

	if !strings.Contains(err.Error(), "agents.codex.approval_policy must be one of") {
		t.Fatalf("expected actionable codex approval_policy error, got %q", err.Error())
	}
}

func TestLoadTrimsAgentExecutionConfigWhitespace(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
default_agent = "claude"

[agents.claude]
permission_mode = " bypassPermissions "

[agents.codex]
sandbox_mode = " workspace-write "
approval_policy = " on-request "
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got := loaded.Config.Agents.Claude.PermissionMode; got != "bypassPermissions" {
		t.Fatalf("expected trimmed claude permission_mode, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.SandboxMode; got != "workspace-write" {
		t.Fatalf("expected trimmed codex sandbox_mode, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.ApprovalPolicy; got != "on-request" {
		t.Fatalf("expected trimmed codex approval_policy, got %q", got)
	}
}
