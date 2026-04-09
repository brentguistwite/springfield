package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/ralph"
)

func writeRalphSpec(t *testing.T, dir string, spec ralph.Spec) string {
	t.Helper()

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatalf("marshal Ralph spec: %v", err)
	}

	path := filepath.Join(dir, "ralph-spec.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write Ralph spec: %v", err)
	}

	return path
}

func writeSpringfieldConfig(t *testing.T, dir string, agent string) {
	t.Helper()

	content := "[project]\ndefault_agent = \"" + agent + "\"\n"
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

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
