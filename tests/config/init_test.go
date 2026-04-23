package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"springfield/internal/core/config"
)

func TestInitCreatesConfigAndRuntimeDir(t *testing.T) {
	dir := t.TempDir()

	result, err := config.Init(dir, []string{"codex", "claude"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Config file should exist and be loadable.
	configPath := filepath.Join(dir, config.FileName)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Runtime dir should exist.
	runtimeDir := filepath.Join(dir, ".springfield")
	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf(".springfield dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".springfield should be a directory")
	}

	// ConfigCreated true, BackupPath empty (no pre-existing config).
	if !result.ConfigCreated {
		t.Error("expected ConfigCreated=true")
	}
	if result.ConfigUpdated {
		t.Error("expected ConfigUpdated=false on fresh init")
	}
	if result.BackupPath != "" {
		t.Errorf("expected BackupPath empty, got %q", result.BackupPath)
	}
	if !result.RuntimeDirCreated {
		t.Error("expected RuntimeDirCreated=true")
	}

	// Created config should be loadable with priority-specific defaults.
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("created config should be loadable: %v", err)
	}
	if loaded.Config.Project.DefaultAgent != "codex" {
		t.Errorf("default_agent: want codex, got %q", loaded.Config.Project.DefaultAgent)
	}
	priority := loaded.Config.EffectivePriority()
	if len(priority) != 2 || priority[0] != "codex" || priority[1] != "claude" {
		t.Errorf("agent_priority: want [codex claude], got %v", priority)
	}

	// Both agent sections always emitted with recommended defaults.
	if got := loaded.Config.Agents.Claude.PermissionMode; got != "bypassPermissions" {
		t.Errorf("claude permission_mode: want bypassPermissions, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.SandboxMode; got != "danger-full-access" {
		t.Errorf("codex sandbox_mode: want danger-full-access, got %q", got)
	}
	if got := loaded.Config.Agents.Codex.ApprovalPolicy; got != "never" {
		t.Errorf("codex approval_policy: want never, got %q", got)
	}
}

func TestInitMergePreservesPlans(t *testing.T) {
	dir := t.TempDir()

	// Pre-create config with a [plans] section.
	configPath := filepath.Join(dir, config.FileName)
	original := `[project]
default_agent = "claude"
agent_priority = ["claude", "codex"]

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"

[plans.release]
agent = "codex"
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	// Re-init with different priority (merge mode, no reset).
	result, err := config.Init(dir, []string{"codex", "claude"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("re-init failed: %v", err)
	}

	if result.BackupPath != "" {
		t.Errorf("expected no backup in merge mode, got %q", result.BackupPath)
	}
	if result.ConfigCreated {
		t.Error("expected ConfigCreated=false on re-init")
	}
	// Priority changed codex→first, so ConfigUpdated should be true.
	if !result.ConfigUpdated {
		t.Error("expected ConfigUpdated=true (priority changed)")
	}

	// [plans.release] must survive.
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load after re-init: %v", err)
	}
	plan, ok := loaded.Config.Plans["release"]
	if !ok {
		t.Error("expected [plans.release] to be preserved after merge")
	} else if plan.Agent != "codex" {
		t.Errorf("plans.release.agent: want codex, got %q", plan.Agent)
	}

	// Priority updated.
	if loaded.Config.Project.DefaultAgent != "codex" {
		t.Errorf("default_agent: want codex, got %q", loaded.Config.Project.DefaultAgent)
	}
}

func TestInitMergeFillsMissingAgentDefaults(t *testing.T) {
	dir := t.TempDir()

	// Pre-create config with only [project] section (no agents).
	configPath := filepath.Join(dir, config.FileName)
	original := `[project]
default_agent = "claude"
agent_priority = ["claude", "codex"]
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("re-init failed: %v", err)
	}

	if result.BackupPath != "" {
		t.Errorf("expected no backup in merge mode, got %q", result.BackupPath)
	}
	// Agents were filled in → ConfigUpdated.
	if !result.ConfigUpdated {
		t.Error("expected ConfigUpdated=true (agent defaults filled)")
	}

	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load after merge: %v", err)
	}
	if loaded.Config.Agents.Claude.PermissionMode != "bypassPermissions" {
		t.Errorf("claude permission_mode: want bypassPermissions, got %q", loaded.Config.Agents.Claude.PermissionMode)
	}
	if loaded.Config.Agents.Codex.SandboxMode != "danger-full-access" {
		t.Errorf("codex sandbox_mode: want danger-full-access, got %q", loaded.Config.Agents.Codex.SandboxMode)
	}
	if loaded.Config.Agents.Codex.ApprovalPolicy != "never" {
		t.Errorf("codex approval_policy: want never, got %q", loaded.Config.Agents.Codex.ApprovalPolicy)
	}
}

func TestInitMergePreservesCustomAgentSettings(t *testing.T) {
	dir := t.TempDir()

	// Pre-create config with a non-default but valid claude permission_mode.
	// "acceptEdits" is valid and differs from the recommended "bypassPermissions",
	// so merge must preserve it and still fill in the absent Codex section.
	configPath := filepath.Join(dir, config.FileName)
	original := `[project]
default_agent = "claude"
agent_priority = ["claude", "codex"]

[agents.claude]
permission_mode = "acceptEdits"
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("re-init failed: %v", err)
	}

	if result.BackupPath != "" {
		t.Errorf("expected no backup in merge mode, got %q", result.BackupPath)
	}
	// Codex defaults were filled in → ConfigUpdated must be true.
	if !result.ConfigUpdated {
		t.Error("expected ConfigUpdated=true (Codex defaults filled in)")
	}

	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load after merge: %v", err)
	}

	// Custom claude setting must be preserved (not overwritten with recommended).
	if loaded.Config.Agents.Claude.PermissionMode != "acceptEdits" {
		t.Errorf("claude permission_mode: want acceptEdits (preserved), got %q", loaded.Config.Agents.Claude.PermissionMode)
	}

	// Codex was absent → should be filled with defaults.
	if loaded.Config.Agents.Codex.SandboxMode != "danger-full-access" {
		t.Errorf("codex sandbox_mode: want danger-full-access, got %q", loaded.Config.Agents.Codex.SandboxMode)
	}
	if loaded.Config.Agents.Codex.ApprovalPolicy != "never" {
		t.Errorf("codex approval_policy: want never, got %q", loaded.Config.Agents.Codex.ApprovalPolicy)
	}
}

func TestInitMergeUpdatesAgentPriority(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, config.FileName)
	original := `[project]
default_agent = "claude"
agent_priority = ["claude"]

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Init(dir, []string{"codex", "claude"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("re-init failed: %v", err)
	}
	if !result.ConfigUpdated {
		t.Error("expected ConfigUpdated=true (priority changed)")
	}

	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load after merge: %v", err)
	}
	priority := loaded.Config.EffectivePriority()
	if len(priority) != 2 || priority[0] != "codex" || priority[1] != "claude" {
		t.Errorf("agent_priority: want [codex claude], got %v", priority)
	}
	if loaded.Config.Project.DefaultAgent != "codex" {
		t.Errorf("default_agent: want codex, got %q", loaded.Config.Project.DefaultAgent)
	}
}

func TestInitMergeIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	// Fresh init.
	_, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Second init with same priority — no changes needed.
	result, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("second init failed: %v", err)
	}
	if result.ConfigUpdated {
		t.Error("expected ConfigUpdated=false on idempotent re-init")
	}
	if result.BackupPath != "" {
		t.Errorf("expected no backup on idempotent re-init, got %q", result.BackupPath)
	}
}

func TestInitMergeBackfillsGeminiDefaults(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, config.FileName)
	original := `[project]
default_agent = "claude"
agent_priority = ["claude", "codex", "gemini"]

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := config.Init(dir, []string{"claude", "codex", "gemini"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Config.Agents.Gemini.ApprovalMode != "yolo" {
		t.Errorf("gemini approval_mode: want yolo, got %q", loaded.Config.Agents.Gemini.ApprovalMode)
	}
	if loaded.Config.Agents.Gemini.SandboxMode != "sandbox-exec" {
		t.Errorf("gemini sandbox_mode: want sandbox-exec, got %q", loaded.Config.Agents.Gemini.SandboxMode)
	}
}

func TestInitFreshScaffoldIncludesGeminiWhenInPriority(t *testing.T) {
	dir := t.TempDir()

	_, err := config.Init(dir, []string{"claude", "codex", "gemini"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "[agents.gemini]") {
		t.Fatalf("expected [agents.gemini] section, got:\n%s", text)
	}
	if !strings.Contains(text, "approval_mode = \"yolo\"") {
		t.Fatalf("expected approval_mode yolo, got:\n%s", text)
	}
}

func TestInitFreshScaffoldOmitsGeminiWhenNotInPriority(t *testing.T) {
	dir := t.TempDir()

	_, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "[agents.gemini]") {
		t.Fatalf("unexpected [agents.gemini] section, got:\n%s", text)
	}
}

func TestInitMergeDoesNotBackfillGeminiWhenNotInPriority(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, config.FileName)
	original := `[project]
default_agent = "claude"
agent_priority = ["claude", "codex"]

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Config.Agents.Gemini.ApprovalMode != "" || loaded.Config.Agents.Gemini.SandboxMode != "" {
		t.Errorf("expected gemini fields empty, got approval_mode=%q sandbox_mode=%q",
			loaded.Config.Agents.Gemini.ApprovalMode, loaded.Config.Agents.Gemini.SandboxMode)
	}

	// Also confirm the TOML on disk doesn't have a [agents.gemini] section.
	data, err := os.ReadFile(filepath.Join(dir, config.FileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), "[agents.gemini]") {
		t.Errorf("unexpected [agents.gemini] section in TOML:\n%s", string(data))
	}
}

func TestInitResetBacksUpAndWritesFresh(t *testing.T) {
	dir := t.TempDir()

	// Pre-create a minimal valid config (with agents) so it round-trips cleanly.
	configPath := filepath.Join(dir, config.FileName)
	original := `[project]
default_agent = "claude"
agent_priority = ["claude", "codex"]

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"

[plans.release]
agent = "codex"
`
	if err := os.WriteFile(configPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{Reset: true})
	if err != nil {
		t.Fatalf("reset init failed: %v", err)
	}

	// BackupPath must be set.
	if result.BackupPath == "" {
		t.Fatal("expected BackupPath to be set on --reset")
	}

	// Backup file must contain original content.
	backed, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if string(backed) != original {
		t.Errorf("backup content mismatch\nwant: %q\ngot:  %q", original, string(backed))
	}

	// New config has fresh scaffold — no [plans.release].
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load after reset: %v", err)
	}
	if _, ok := loaded.Config.Plans["release"]; ok {
		t.Error("expected [plans.release] to be gone after --reset")
	}
	if loaded.Config.Project.DefaultAgent != "claude" {
		t.Errorf("default_agent: want claude, got %q", loaded.Config.Project.DefaultAgent)
	}
}

func TestInitBackupPathFormat(t *testing.T) {
	dir := t.TempDir()

	configPath := filepath.Join(dir, config.FileName)
	stub := `[project]
default_agent = "claude"
agent_priority = ["claude", "codex"]

[agents.claude]
permission_mode = "bypassPermissions"

[agents.codex]
sandbox_mode = "danger-full-access"
approval_policy = "never"
`
	if err := os.WriteFile(configPath, []byte(stub), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{Reset: true})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if result.BackupPath == "" {
		t.Fatal("expected BackupPath to be set")
	}

	base := filepath.Base(result.BackupPath)
	pattern := regexp.MustCompile(`^springfield\.toml\.bak-\d{8}T\d{6}Z$`)
	if !pattern.MatchString(base) {
		t.Errorf("backup filename %q does not match expected pattern springfield.toml.bak-<ISO8601>", base)
	}
}

func TestInitAtomicWriteLeavesNoOrphan(t *testing.T) {
	dir := t.TempDir()

	_, err := config.Init(dir, []string{"claude", "codex"}, config.InitOptions{})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("orphan temp file found: %s", e.Name())
		}
	}
}
