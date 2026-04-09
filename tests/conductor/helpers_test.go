package conductor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/config"
	"springfield/internal/features/conductor"
	"springfield/internal/storage"
)

func writeProjectConfig(t *testing.T, root string) string {
	t.Helper()

	path := filepath.Join(root, config.FileName)
	body := `[project]
default_agent = "claude"
`
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	return path
}

func writeConductorConfig(t *testing.T, root string, cfg *conductor.Config) {
	t.Helper()

	runtime, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	if err := runtime.WriteJSON("execution/config.json", cfg); err != nil {
		t.Fatalf("write conductor config: %v", err)
	}
}

func writeConductorState(t *testing.T, root string, state *conductor.State) {
	t.Helper()

	runtime, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	if err := runtime.WriteJSON("execution/state.json", state); err != nil {
		t.Fatalf("write conductor state: %v", err)
	}
}

func writeLegacyConductorConfig(t *testing.T, root string, cfg *conductor.Config) {
	t.Helper()

	runtime, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	if err := runtime.WriteJSON("conductor/config.json", cfg); err != nil {
		t.Fatalf("write legacy conductor config: %v", err)
	}
}

func writeLegacyConductorState(t *testing.T, root string, state *conductor.State) {
	t.Helper()

	runtime, err := storage.FromRoot(root)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	if err := runtime.WriteJSON("conductor/state.json", state); err != nil {
		t.Fatalf("write legacy conductor state: %v", err)
	}
}

func sequentialOnlyConfig() *conductor.Config {
	return &conductor.Config{
		PlansDir:                   conductor.TrackedPlansDir,
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		FallbackTool:               "codex",
		Sequential: []string{
			"01-bootstrap",
			"02-config",
			"03-runtime",
		},
	}
}

func mixedConfig() *conductor.Config {
	return &conductor.Config{
		PlansDir:                   conductor.TrackedPlansDir,
		WorktreeBase:               ".worktrees",
		MaxRetries:                 1,
		SingleWorkstreamIterations: 30,
		SingleWorkstreamTimeout:    1800,
		Tool:                       "claude",
		Batches:                    [][]string{{"batch-a", "batch-b"}, {"batch-c"}},
		Sequential:                 []string{"seq-1", "seq-2"},
	}
}

func hideAgentBinariesFromPath(t *testing.T) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())
}
