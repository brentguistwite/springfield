package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitGitignoreIncludesLocalToml verifies init's gitignore block ignores
// the per-operator springfield.local.toml control file (Fix 3).
func TestInitGitignoreIncludesLocalToml(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude", "--tracked-gitignore"); err != nil {
		t.Fatalf("init: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), "springfield.local.toml") {
		t.Errorf("expected springfield.local.toml ignored, got:\n%s", string(data))
	}
}
