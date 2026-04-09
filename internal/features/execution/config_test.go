package execution_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/execution"
)

func TestSetupWritesSpringfieldOwnedConfig(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	input := execution.Defaults()
	input.PlansDir = execution.TrackedPlansDir

	result, err := execution.Setup(root, []string{"claude", "codex"}, input)
	if err != nil {
		t.Fatalf("execution.Setup() error: %v", err)
	}
	if !result.Created {
		t.Fatal("expected Created=true")
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"plans_dir": "springfield/plans"`) {
		t.Fatalf("expected Springfield tracked plans path, got:\n%s", content)
	}
	if !strings.Contains(content, `"single_workstream_iterations": 50`) {
		t.Fatalf("expected Springfield single_workstream_iterations key, got:\n%s", content)
	}
	if strings.Contains(content, "ralph_iterations") {
		t.Fatalf("did not expect legacy ralph_iterations key, got:\n%s", content)
	}
}

func TestLoadReadsLegacyConfigTerms(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".springfield", "conductor", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	body := `{
  "plans_dir": ".conductor/plans",
  "worktree_base": ".worktrees",
  "max_retries": 2,
  "ralph_iterations": 21,
  "ralph_timeout": 900,
  "tool": "claude",
  "fallback_tool": "codex",
  "sequential": ["01-bootstrap"],
  "batches": []
}`
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	writeProjectConfig(t, root)

	cfg, err := execution.Load(root)
	if err != nil {
		t.Fatalf("execution.Load() error: %v", err)
	}

	if cfg.PlansDir != ".conductor/plans" {
		t.Fatalf("PlansDir = %q, want legacy tracked path", cfg.PlansDir)
	}
	if !execution.IsTrackedPlansDir(cfg.PlansDir) {
		t.Fatal("expected legacy tracked path to be treated as tracked")
	}
	if cfg.SingleWorkstreamIterations != 21 {
		t.Fatalf("SingleWorkstreamIterations = %d, want 21", cfg.SingleWorkstreamIterations)
	}
	if cfg.SingleWorkstreamTimeout != 900 {
		t.Fatalf("SingleWorkstreamTimeout = %d, want 900", cfg.SingleWorkstreamTimeout)
	}
}

func writeProjectConfig(t *testing.T, root string) {
	t.Helper()

	body := `[project]
default_agent = "claude"
`
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}
}
