package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/config"
	"springfield/internal/features/bootstrap"
)

func TestStatusLeavesExecutionNotReadyWhenConfigMalformed(t *testing.T) {
	root := t.TempDir()
	writeBootstrapConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
	}, "\n"))
	if err := os.MkdirAll(filepath.Join(root, ".springfield", "execution"), 0o755); err != nil {
		t.Fatalf("mkdir execution dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".springfield", "execution", "config.json"), []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("write malformed config: %v", err)
	}

	service := bootstrap.NewService(func() (string, error) { return root, nil }, nil)
	status := service.Status()

	if !status.ConfigPresent {
		t.Fatalf("expected config present, got %#v", status)
	}
	if !status.RuntimePresent {
		t.Fatalf("expected runtime present, got %#v", status)
	}
	if status.ExecutionReady {
		t.Fatalf("expected execution not ready when config is malformed, got %#v", status)
	}
}

func TestEnsureRecommendedExecutionDefaultsWritesRecommendedWhenUnset(t *testing.T) {
	root := t.TempDir()
	writeBootstrapConfig(t, root, strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		"",
	}, "\n"))

	service := bootstrap.NewService(func() (string, error) { return root, nil }, nil)
	if err := service.EnsureRecommendedExecutionDefaults(); err != nil {
		t.Fatalf("EnsureRecommendedExecutionDefaults: %v", err)
	}

	loaded, err := config.LoadFrom(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := loaded.Config.Agents.Claude.PermissionMode; got != "bypassPermissions" {
		t.Fatalf("claude permission mode = %q, want bypassPermissions", got)
	}
	if got := loaded.Config.Agents.Codex.SandboxMode; got != "danger-full-access" {
		t.Fatalf("codex sandbox mode = %q, want danger-full-access", got)
	}
	if got := loaded.Config.Agents.Codex.ApprovalPolicy; got != "never" {
		t.Fatalf("codex approval policy = %q, want never", got)
	}
}

func writeBootstrapConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir runtime dir: %v", err)
	}
}
