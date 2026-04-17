package config_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"springfield/internal/core/config"
)

func TestInitCreatesConfigAndRuntimeDir(t *testing.T) {
	dir := t.TempDir()

	result, err := config.Init(dir, []string{"codex", "claude"})
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Config file should exist and be loadable
	configPath := filepath.Join(dir, config.FileName)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config file not created: %v", err)
	}

	// Runtime dir should exist
	runtimeDir := filepath.Join(dir, ".springfield")
	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf(".springfield dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".springfield should be a directory")
	}

	// RuntimeDirCreated true, BackupPath empty (no pre-existing config)
	if !result.RuntimeDirCreated {
		t.Error("expected RuntimeDirCreated=true")
	}
	if result.BackupPath != "" {
		t.Errorf("expected BackupPath empty, got %q", result.BackupPath)
	}

	// Created config should be loadable with priority-specific defaults
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

	// Both agent sections always emitted with recommended defaults
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

func TestInitBacksUpExistingConfig(t *testing.T) {
	dir := t.TempDir()

	// Pre-create config with custom content
	configPath := filepath.Join(dir, config.FileName)
	original := []byte("[project]\ndefault_agent = \"custom-agent\"\n")
	if err := os.WriteFile(configPath, original, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Init(dir, []string{"claude", "codex"})
	if err != nil {
		t.Fatalf("re-init failed: %v", err)
	}

	// BackupPath must be set
	if result.BackupPath == "" {
		t.Fatal("expected BackupPath to be set")
	}

	// Backup file must contain original content
	backed, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if string(backed) != string(original) {
		t.Errorf("backup content mismatch\nwant: %q\ngot:  %q", string(original), string(backed))
	}

	// New config file has fresh content (not the custom agent)
	loaded, err := config.LoadFrom(dir)
	if err != nil {
		t.Fatalf("load after re-init: %v", err)
	}
	if loaded.Config.Project.DefaultAgent == "custom-agent" {
		t.Error("new config still has old default_agent; expected fresh scaffold")
	}
	if loaded.Config.Project.DefaultAgent != "claude" {
		t.Errorf("new default_agent: want claude, got %q", loaded.Config.Project.DefaultAgent)
	}
}

func TestInitBackupPathFormat(t *testing.T) {
	dir := t.TempDir()

	// Pre-create config so a backup will be made
	configPath := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(configPath, []byte("[project]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Init(dir, []string{"claude", "codex"})
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
