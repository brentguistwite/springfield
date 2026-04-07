package conductor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/storage"
)

func TestSetup_GeneratesConfigWhenNoneExists(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	opts := conductor.SetupDefaults()
	opts.Tool = "claude"
	opts.Sequential = []string{"plan-a", "plan-b"}

	result, err := conductor.Setup(root, opts)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	if !result.Created {
		t.Error("expected Created=true for fresh setup")
	}
	if result.Reused {
		t.Error("expected Reused=false for fresh setup")
	}

	// Verify config was written and is loadable
	rt, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("storage.FromRoot: %v", err)
	}

	var cfg conductor.Config
	if err := rt.ReadJSON("conductor/config.json", &cfg); err != nil {
		t.Fatalf("reading generated config: %v", err)
	}

	if cfg.Tool != "claude" {
		t.Errorf("Tool = %q, want %q", cfg.Tool, "claude")
	}
	if len(cfg.Sequential) != 2 || cfg.Sequential[0] != "plan-a" {
		t.Errorf("Sequential = %v, want [plan-a plan-b]", cfg.Sequential)
	}
}

func TestSetup_ReusesExistingValidConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	existing := sequentialOnlyConfig()
	writeConductorConfig(t, root, existing)

	opts := conductor.SetupDefaults()
	opts.Tool = "codex" // different from existing — should NOT overwrite

	result, err := conductor.Setup(root, opts)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	if result.Created {
		t.Error("expected Created=false when valid config exists")
	}
	if !result.Reused {
		t.Error("expected Reused=true when valid config exists")
	}

	// Verify original config is preserved
	rt, _ := storage.FromRoot(root)
	var cfg conductor.Config
	rt.ReadJSON("conductor/config.json", &cfg)

	if cfg.Tool != "claude" {
		t.Errorf("Tool = %q, want original %q", cfg.Tool, "claude")
	}
}

func TestIsReady_FalseWhenNoConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	ready, err := conductor.IsReady(root)
	if err != nil {
		t.Fatalf("IsReady() error: %v", err)
	}
	if ready {
		t.Error("expected IsReady=false when no config exists")
	}
}

func TestIsReady_TrueWhenValidConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	ready, err := conductor.IsReady(root)
	if err != nil {
		t.Fatalf("IsReady() error: %v", err)
	}
	if !ready {
		t.Error("expected IsReady=true when valid config exists")
	}
}

func TestSetupDefaults_ReasonableValues(t *testing.T) {
	opts := conductor.SetupDefaults()

	if opts.PlansDir == "" {
		t.Error("PlansDir should have a default")
	}
	if opts.WorktreeBase == "" {
		t.Error("WorktreeBase should have a default")
	}
	if opts.MaxRetries < 1 {
		t.Error("MaxRetries should be >= 1")
	}
	if opts.RalphIterations < 1 {
		t.Error("RalphIterations should be >= 1")
	}
	if opts.RalphTimeout < 1 {
		t.Error("RalphTimeout should be >= 1")
	}
}

func TestSetup_FailsWithoutProject(t *testing.T) {
	root := t.TempDir()
	// No springfield.toml

	opts := conductor.SetupDefaults()
	_, err := conductor.Setup(root, opts)
	if err == nil {
		t.Fatal("expected error when project not initialized")
	}
}

func TestSetup_ConfigPathRelativeToProject(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	opts := conductor.SetupDefaults()
	opts.Tool = "claude"

	result, err := conductor.Setup(root, opts)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	expectedPath := filepath.Join(root, ".springfield", "conductor", "config.json")
	if result.Path != expectedPath {
		t.Errorf("Path = %q, want %q", result.Path, expectedPath)
	}

	// File must actually exist
	if _, err := os.Stat(result.Path); err != nil {
		t.Errorf("config file does not exist at %s: %v", result.Path, err)
	}
}

func TestSetup_WritesCanonicalEmptyArrays(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	opts := conductor.SetupDefaults()
	opts.Tool = "claude"

	result, err := conductor.Setup(root, opts)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error: %v", result.Path, err)
	}

	json := string(data)
	if strings.Contains(json, `"sequential": null`) {
		t.Fatalf("setup wrote null sequential array: %s", json)
	}
	if strings.Contains(json, `"batches": null`) {
		t.Fatalf("setup wrote null batches array: %s", json)
	}
	if !strings.Contains(json, `"sequential": []`) {
		t.Fatalf("setup did not write canonical empty sequential array: %s", json)
	}
	if !strings.Contains(json, `"batches": []`) {
		t.Fatalf("setup did not write canonical empty batches array: %s", json)
	}
}
