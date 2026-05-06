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
agent_priority = ["claude"]
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

func writeRegisteredPlanUnitConfig(t *testing.T, root string, ids []string) {
	t.Helper()

	planDir := filepath.Join(root, conductor.TrackedPlansDir)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}

	units := make([]conductor.PlanUnit, 0, len(ids))
	for i, id := range ids {
		if err := os.WriteFile(filepath.Join(planDir, id+".md"), []byte("# "+id+"\n"), 0o644); err != nil {
			t.Fatalf("write plan %s: %v", id, err)
		}
		units = append(units, conductor.PlanUnit{
			ID:    id,
			Path:  conductor.TrackedPlansDir + "/" + id + ".md",
			Order: i + 1,
		})
	}

	writeConductorConfig(t, root, &conductor.Config{
		PlansDir:                   conductor.TrackedPlansDir,
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  units,
	})
}

func planUnitConfig(ids ...string) *conductor.Config {
	units := make([]conductor.PlanUnit, 0, len(ids))
	for i, id := range ids {
		units = append(units, conductor.PlanUnit{
			ID:    id,
			Path:  conductor.TrackedPlansDir + "/" + id + ".md",
			Order: i + 1,
		})
	}
	return &conductor.Config{
		PlansDir:                   conductor.TrackedPlansDir,
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  units,
	}
}

func hideAgentBinariesFromPath(t *testing.T) {
	t.Helper()

	t.Setenv("PATH", t.TempDir())
}
