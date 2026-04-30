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
agent_priority = ["claude"]

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

	if got := loaded.Config.AgentForPlan("release"); got != "codex" {
		t.Fatalf("expected plan override codex, got %q", got)
	}

	if got := loaded.Config.AgentForPlan("missing"); got != "claude" {
		t.Fatalf("expected fallback agent claude, got %q", got)
	}
}

func TestAgentForPlanReadsPriorityZero(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{AgentPriority: []string{"claude", "codex"}},
	}
	if got := cfg.AgentForPlan("missing"); got != "claude" {
		t.Fatalf("AgentForPlan(missing) = %q, want claude", got)
	}
}

func TestAgentForPlanRespectsPlanOverride(t *testing.T) {
	cfg := config.Config{
		Project: config.ProjectConfig{AgentPriority: []string{"claude"}},
		Plans:   map[string]config.PlanConfig{"release": {Agent: "codex"}},
	}
	if got := cfg.AgentForPlan("release"); got != "codex" {
		t.Fatalf("AgentForPlan(release) = %q, want codex", got)
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

func TestLoadFromRejectsUnsupportedAgentInPriority(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
agent_priority = ["bogus"]
`)

	_, err := config.LoadFrom(root)
	if err == nil {
		t.Fatal("expected invalid config error")
	}

	var invalidErr *config.InvalidConfigError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("expected InvalidConfigError, got %T", err)
	}

	if !strings.Contains(err.Error(), "project.agent_priority") {
		t.Fatalf("expected error to mention project.agent_priority, got %q", err.Error())
	}

	if !strings.Contains(err.Error(), filepath.Join(root, config.FileName)) {
		t.Fatalf("expected error to mention config path, got %q", err.Error())
	}
}

func TestLoadFromRejectsDuplicateAgentInPriority(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
agent_priority = ["claude", "claude"]
`)

	_, err := config.LoadFrom(root)
	if err == nil {
		t.Fatal("expected invalid config error")
	}
	if !strings.Contains(err.Error(), "duplicate agent") {
		t.Fatalf("expected duplicate-agent error, got %q", err.Error())
	}
}

func TestLoadFromAcceptsEmptyPriority(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("expected empty priority to be valid (unconfigured), got %v", err)
	}
	if got := loaded.Config.AgentForPlan("anything"); got != "" {
		t.Fatalf("AgentForPlan with empty priority = %q, want empty", got)
	}
}

func TestLoadParsesClaudeExecutionConfig(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
agent_priority = ["claude"]

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
agent_priority = ["codex"]

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
agent_priority = ["claude"]

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
agent_priority = ["codex"]

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

func TestLoadParsesGeminiSection(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `[project]
agent_priority = ["claude","codex","gemini"]
[agents.gemini]
approval_mode = "yolo"
sandbox_mode = "sandbox-exec"
model = "pro"
`
	if err := os.WriteFile(filepath.Join(dir, "springfield.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := loaded.Config.Agents.Gemini.ApprovalMode; got != "yolo" {
		t.Fatalf("approval_mode: want yolo, got %q", got)
	}
	if got := loaded.Config.Agents.Gemini.SandboxMode; got != "sandbox-exec" {
		t.Fatalf("sandbox_mode: want sandbox-exec, got %q", got)
	}
	if got := loaded.Config.Agents.Gemini.Model; got != "pro" {
		t.Fatalf("model: want pro, got %q", got)
	}
	settings := loaded.Config.ExecutionSettingsForAgent(string(agents.AgentGemini))
	if settings.Gemini.ApprovalMode != "yolo" {
		t.Fatalf("resolved gemini approval_mode: want yolo, got %q", settings.Gemini.ApprovalMode)
	}
	if settings.Gemini.SandboxMode != "sandbox-exec" {
		t.Fatalf("resolved gemini sandbox_mode: want sandbox-exec, got %q", settings.Gemini.SandboxMode)
	}
	if settings.Gemini.Model != "pro" {
		t.Fatalf("resolved gemini model: want pro, got %q", settings.Gemini.Model)
	}
}

func TestLoadRejectsUnknownGeminiApprovalMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "springfield.toml"), []byte(
		"[project]\nagent_priority = [\"gemini\"]\n[agents.gemini]\napproval_mode = \"invalid\"\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := config.LoadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "agents.gemini.approval_mode must be one of") {
		t.Fatalf("expected actionable gemini approval_mode error, got %v", err)
	}
}

func TestLoadRejectsUnknownGeminiSandboxMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "springfield.toml"), []byte(
		"[project]\nagent_priority = [\"gemini\"]\n[agents.gemini]\nsandbox_mode = \"invalid\"\n",
	), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := config.LoadFrom(dir)
	if err == nil || !strings.Contains(err.Error(), "agents.gemini.sandbox_mode must be one of") {
		t.Fatalf("expected actionable gemini sandbox_mode error, got %v", err)
	}
}

func TestLoadTrimsGeminiExecutionConfigWhitespace(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
agent_priority = ["gemini"]

[agents.gemini]
approval_mode = "  yolo  "
sandbox_mode = "  sandbox-exec  "
model = "  pro  "
`)
	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := loaded.Config.Agents.Gemini.ApprovalMode; got != "yolo" {
		t.Fatalf("trimmed approval_mode: got %q", got)
	}
	if got := loaded.Config.Agents.Gemini.SandboxMode; got != "sandbox-exec" {
		t.Fatalf("trimmed sandbox_mode: got %q", got)
	}
	if got := loaded.Config.Agents.Gemini.Model; got != "pro" {
		t.Fatalf("trimmed model: got %q", got)
	}
}

func TestExecutionModesReturnsRecommendedForGeminiYoloSandboxExec(t *testing.T) {
	cfg := config.Config{
		Agents: config.AgentsConfig{
			Gemini: config.GeminiAgentConfig{
				ApprovalMode: "yolo",
				SandboxMode:  "sandbox-exec",
			},
		},
	}
	if got := cfg.ExecutionModes().Gemini; got != config.ExecutionModeRecommended {
		t.Fatalf("gemini mode: want %q, got %q", config.ExecutionModeRecommended, got)
	}
}

func TestExecutionModesReturnsOffForEmptyGemini(t *testing.T) {
	cfg := config.Config{}
	if got := cfg.ExecutionModes().Gemini; got != config.ExecutionModeOff {
		t.Fatalf("gemini mode: want %q, got %q", config.ExecutionModeOff, got)
	}
}

func TestExecutionModesReturnsCustomForGeminiPartial(t *testing.T) {
	cfg := config.Config{
		Agents: config.AgentsConfig{
			Gemini: config.GeminiAgentConfig{
				ApprovalMode: "plan",
			},
		},
	}
	if got := cfg.ExecutionModes().Gemini; got != config.ExecutionModeCustom {
		t.Fatalf("gemini mode: want %q, got %q", config.ExecutionModeCustom, got)
	}
}

func TestHasAnyExecutionSettingsTrueWhenGeminiSet(t *testing.T) {
	cfg := config.Config{
		Agents: config.AgentsConfig{
			Gemini: config.GeminiAgentConfig{ApprovalMode: "yolo"},
		},
	}
	if !cfg.HasAnyExecutionSettings() {
		t.Fatal("expected HasAnyExecutionSettings true when gemini approval_mode set")
	}
}

func TestApplyExecutionModeRecommendedGemini(t *testing.T) {
	cfg := config.Config{}
	cfg.ApplyExecutionMode("gemini", config.ExecutionModeRecommended)
	if cfg.Agents.Gemini.ApprovalMode != "yolo" {
		t.Fatalf("approval_mode: got %q", cfg.Agents.Gemini.ApprovalMode)
	}
	if cfg.Agents.Gemini.SandboxMode != "sandbox-exec" {
		t.Fatalf("sandbox_mode: got %q", cfg.Agents.Gemini.SandboxMode)
	}
}

func TestApplyExecutionModeOffGemini(t *testing.T) {
	cfg := config.Config{
		Agents: config.AgentsConfig{
			Gemini: config.GeminiAgentConfig{ApprovalMode: "yolo", SandboxMode: "sandbox-exec", Model: "pro"},
		},
	}
	cfg.ApplyExecutionMode("gemini", config.ExecutionModeOff)
	if cfg.Agents.Gemini.ApprovalMode != "" || cfg.Agents.Gemini.SandboxMode != "" || cfg.Agents.Gemini.Model != "" {
		t.Fatalf("expected gemini fields cleared, got %+v", cfg.Agents.Gemini)
	}
}

func TestApplyRecommendedDefaultsSetsGemini(t *testing.T) {
	cfg := config.Config{}
	cfg.ApplyRecommendedExecutionDefaults()
	if cfg.Agents.Gemini.ApprovalMode != "yolo" {
		t.Fatalf("approval_mode: got %q", cfg.Agents.Gemini.ApprovalMode)
	}
	if cfg.Agents.Gemini.SandboxMode != "sandbox-exec" {
		t.Fatalf("sandbox_mode: got %q", cfg.Agents.Gemini.SandboxMode)
	}
}

func TestLoadRejectsUnknownCodexApprovalPolicy(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
agent_priority = ["codex"]

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
agent_priority = ["claude"]

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

func TestExecutionModesRecommended(t *testing.T) {
	cfg := config.Config{
		Agents: config.AgentsConfig{
			Claude: config.ClaudeAgentConfig{PermissionMode: "bypassPermissions"},
			Codex: config.CodexAgentConfig{
				SandboxMode:    "danger-full-access",
				ApprovalPolicy: "never",
			},
		},
	}

	got := cfg.ExecutionModes()
	if got.Claude != config.ExecutionModeRecommended {
		t.Fatalf("claude mode: want %q, got %q", config.ExecutionModeRecommended, got.Claude)
	}
	if got.Codex != config.ExecutionModeRecommended {
		t.Fatalf("codex mode: want %q, got %q", config.ExecutionModeRecommended, got.Codex)
	}
}

func TestExecutionModesOff(t *testing.T) {
	cfg := config.Config{}

	got := cfg.ExecutionModes()
	if got.Claude != config.ExecutionModeOff {
		t.Fatalf("claude mode: want %q, got %q", config.ExecutionModeOff, got.Claude)
	}
	if got.Codex != config.ExecutionModeOff {
		t.Fatalf("codex mode: want %q, got %q", config.ExecutionModeOff, got.Codex)
	}
}

func TestExecutionModesCustom(t *testing.T) {
	cfg := config.Config{
		Agents: config.AgentsConfig{
			Claude: config.ClaudeAgentConfig{PermissionMode: "plan"},
			Codex: config.CodexAgentConfig{
				SandboxMode:    "danger-full-access",
				ApprovalPolicy: "on-request",
			},
		},
	}

	got := cfg.ExecutionModes()
	if got.Claude != config.ExecutionModeCustom {
		t.Fatalf("claude mode: want %q, got %q", config.ExecutionModeCustom, got.Claude)
	}
	if got.Codex != config.ExecutionModeCustom {
		t.Fatalf("codex mode: want %q, got %q", config.ExecutionModeCustom, got.Codex)
	}
}

func TestKeepAwakeEnabledDefaultsTrue(t *testing.T) {
	cfg := config.Config{}
	if !cfg.KeepAwakeEnabled() {
		t.Fatal("KeepAwakeEnabled should be true when [start] is absent")
	}
}

func TestKeepAwakeEnabledOptOut(t *testing.T) {
	f := false
	cfg := config.Config{Start: config.StartConfig{KeepAwake: &f}}
	if cfg.KeepAwakeEnabled() {
		t.Fatal("KeepAwakeEnabled should be false when keep_awake = false")
	}
}

func TestKeepAwakeEnabledExplicitTrue(t *testing.T) {
	tr := true
	cfg := config.Config{Start: config.StartConfig{KeepAwake: &tr}}
	if !cfg.KeepAwakeEnabled() {
		t.Fatal("KeepAwakeEnabled should be true when keep_awake = true")
	}
}

func TestLoadParsesStartKeepAwake(t *testing.T) {
	root := t.TempDir()
	writeConfigFile(t, root, `
[project]
agent_priority = ["claude"]

[start]
keep_awake = false
`)

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.Config.KeepAwakeEnabled() {
		t.Fatal("expected KeepAwakeEnabled false when keep_awake = false in toml")
	}
}
