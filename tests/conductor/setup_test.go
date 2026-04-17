package conductor_test

import (
	"errors"
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
	if err := rt.ReadJSON("execution/config.json", &cfg); err != nil {
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
	rt.ReadJSON("execution/config.json", &cfg)

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
	if opts.SingleWorkstreamIterations < 1 {
		t.Error("SingleWorkstreamIterations should be >= 1")
	}
	if opts.SingleWorkstreamTimeout < 1 {
		t.Error("SingleWorkstreamTimeout should be >= 1")
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

	expectedPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if result.Path != expectedPath {
		t.Errorf("Path = %q, want %q", result.Path, expectedPath)
	}

	// File must actually exist
	if _, err := os.Stat(result.Path); err != nil {
		t.Errorf("config file does not exist at %s: %v", result.Path, err)
	}
}

func TestUpdateConfig_OverwritesExisting(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	// First: setup with defaults
	defaults := conductor.SetupDefaults()
	defaults.Tool = "claude"
	_, err := conductor.Setup(root, defaults)
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	// Then: update with new values
	updateOpts := conductor.SetupOptions{
		Tool:                       "codex",
		PlansDir:                   conductor.TrackedPlansDir,
		MaxRetries:                 5,
		SingleWorkstreamIterations: 100,
		SingleWorkstreamTimeout:    7200,
		WorktreeBase:               ".custom-worktrees",
	}

	result, err := conductor.UpdateConfig(root, updateOpts)
	if err != nil {
		t.Fatalf("UpdateConfig() error: %v", err)
	}

	if !result.Updated {
		t.Error("expected Updated=true")
	}
	if result.Path == "" {
		t.Error("expected non-empty Path")
	}
	// Verify updated config by loading it
	rt, _ := storage.FromRoot(root)
	var cfg conductor.Config
	if err := rt.ReadJSON("execution/config.json", &cfg); err != nil {
		t.Fatalf("reading updated config: %v", err)
	}

	if cfg.Tool != "codex" {
		t.Errorf("Tool = %q, want %q", cfg.Tool, "codex")
	}
	if cfg.PlansDir != conductor.TrackedPlansDir {
		t.Errorf("PlansDir = %q, want %q", cfg.PlansDir, conductor.TrackedPlansDir)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", cfg.MaxRetries)
	}
	if cfg.SingleWorkstreamIterations != 100 {
		t.Errorf("SingleWorkstreamIterations = %d, want 100", cfg.SingleWorkstreamIterations)
	}
	if cfg.SingleWorkstreamTimeout != 7200 {
		t.Errorf("SingleWorkstreamTimeout = %d, want 7200", cfg.SingleWorkstreamTimeout)
	}
	if cfg.WorktreeBase != ".custom-worktrees" {
		t.Errorf("WorktreeBase = %q, want %q", cfg.WorktreeBase, ".custom-worktrees")
	}

	data, err := os.ReadFile(filepath.Join(root, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile updated config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"single_workstream_iterations": 100`) {
		t.Fatalf("expected Springfield-owned key in updated config, got:\n%s", content)
	}
	if strings.Contains(content, "ralph_iterations") {
		t.Fatalf("did not expect legacy ralph_iterations key after update, got:\n%s", content)
	}
}

func TestUpdateConfig_FailsWhenNoExistingConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	opts := conductor.SetupDefaults()
	opts.Tool = "claude"

	_, err := conductor.UpdateConfig(root, opts)
	if err == nil {
		t.Fatal("expected error when no existing config")
	}
	if !strings.Contains(err.Error(), "no existing Springfield execution config") {
		t.Errorf("error = %q, want message about no existing config", err.Error())
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

func TestLoadProjectRejectsLegacyConfigPathAndTerms(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	configPath := filepath.Join(root, ".springfield", "conductor", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	body := `{
  "plans_dir": ".conductor/plans",
  "worktree_base": ".worktrees",
  "max_retries": 2,
  "ralph_iterations": 9,
  "ralph_timeout": 600,
  "tool": "claude",
  "sequential": ["01-bootstrap"],
  "batches": []
}`
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	_, err := conductor.LoadProject(root)
	if err == nil {
		t.Fatal("expected legacy conductor config path to be ignored")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestSetupCreatesCanonicalConfigWhenLegacyConfigExists(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeLegacyConductorConfig(t, root, sequentialOnlyConfig())

	result, err := conductor.Setup(root, conductor.SetupDefaults())
	if err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	if !result.Created {
		t.Fatal("expected canonical config to be created")
	}
	if got, want := result.Path, filepath.Join(root, ".springfield", "execution", "config.json"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestUpdateConfigRejectsLegacyConfigPath(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeLegacyConductorConfig(t, root, sequentialOnlyConfig())

	updateOpts := conductor.SetupOptions{
		Tool:                       "codex",
		PlansDir:                   conductor.TrackedPlansDir,
		MaxRetries:                 5,
		SingleWorkstreamIterations: 100,
		SingleWorkstreamTimeout:    7200,
		WorktreeBase:               ".custom-worktrees",
	}

	_, err := conductor.UpdateConfig(root, updateOpts)
	if err == nil {
		t.Fatal("expected update to reject legacy-only config path")
	}
}
