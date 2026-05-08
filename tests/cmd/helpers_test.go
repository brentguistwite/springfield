package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/features/conductor"
)

// fakeDetector implements cmd.Detector for prompt tests. Missing entries
// default to DetectionStatusMissing so tests only need to specify positive
// cases.
type fakeDetector struct {
	statuses map[agents.ID]agents.DetectionStatus
}

func (f fakeDetector) Detect(id agents.ID) agents.DetectionStatus {
	if status, ok := f.statuses[id]; ok {
		return status
	}
	return agents.DetectionStatusMissing
}

func writeSpringfieldConfig(t *testing.T, dir string, agent string) {
	t.Helper()

	// allow_protected_base = true so the guard does not block integration
	// tests that init their tempdir repos on the default "main" branch.
	// The guard itself is exercised in planrun manager tests.
	content := "[project]\nagent_priority = [\"" + agent + "\"]\nallow_protected_base = true\n"
	path := filepath.Join(dir, "springfield.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}
}

func writeConductorConfigBinary(t *testing.T, root string, cfg *conductor.Config) {
	t.Helper()

	dir := filepath.Join(root, ".springfield", "execution")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir execution: %v", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal conductor config: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		t.Fatalf("write execution config: %v", err)
	}
}

func writePlanFileBinary(t *testing.T, root, plansDir, name, content string) {
	t.Helper()

	dir := filepath.Join(root, plansDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
}

