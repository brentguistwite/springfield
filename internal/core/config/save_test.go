package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSavePreservesSetupPortsVerify pins that Save round-trips the committed
// [setup], [ports], and [verify] blocks. These live in the shared saveConfig
// struct rather than being re-encoded from Config, so a missing mirror field
// silently drops the block on write — the regression this guards against.
func TestSavePreservesSetupPortsVerify(t *testing.T) {
	dir := t.TempDir()
	body := `
[project]
agent_priority = ["claude"]

[verify]
enabled = true
command = "go test ./..."
timeout = "10m"
max_verify_iterations = 3

[setup]
enabled = true
command = "npm install"
teardown = "docker compose down"
timeout = "5m"

[ports]
base = 42000
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if err := Save(loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom after Save: %v", err)
	}
	if got, want := reloaded.Config.Verify, loaded.Config.Verify; got != want {
		t.Errorf("Verify block dropped/changed by Save: got %+v, want %+v", got, want)
	}
	if got, want := reloaded.Config.Setup, loaded.Config.Setup; got != want {
		t.Errorf("Setup block dropped/changed by Save: got %+v, want %+v", got, want)
	}
	if got, want := reloaded.Config.Ports, loaded.Config.Ports; got != want {
		t.Errorf("Ports block dropped/changed by Save: got %+v, want %+v", got, want)
	}
}

// TestInitMergePreservesSetupPortsVerify is the end-to-end regression: a
// merge-mode `springfield init` that changes agent priority re-saves the config
// and must not clobber a project's committed [setup]/[ports]/[verify] blocks.
func TestInitMergePreservesSetupPortsVerify(t *testing.T) {
	dir := t.TempDir()
	body := `
[project]
agent_priority = ["claude"]

[verify]
enabled = true
command = "go test ./..."

[setup]
enabled = true
command = "npm install"

[ports]
base = 42000
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Change priority so merge mode marks the config changed and calls Save.
	if _, err := Init(dir, []string{"codex", "claude"}, InitOptions{}); err != nil {
		t.Fatalf("Init merge: %v", err)
	}

	reloaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom after Init: %v", err)
	}
	if !reloaded.Config.Setup.ShouldRun() {
		t.Errorf("Setup block dropped by merge-mode Init: %+v", reloaded.Config.Setup)
	}
	if reloaded.Config.Setup.Command != "npm install" {
		t.Errorf("Setup.Command = %q, want %q", reloaded.Config.Setup.Command, "npm install")
	}
	if !reloaded.Config.Verify.Enabled || reloaded.Config.Verify.Command != "go test ./..." {
		t.Errorf("Verify block dropped by merge-mode Init: %+v", reloaded.Config.Verify)
	}
	if got := reloaded.Config.Ports.BaseOrDefault(); got != 42000 {
		t.Errorf("Ports.BaseOrDefault = %d, want 42000 (block dropped by Init)", got)
	}
}

// TestSaveOmitsUnconfiguredBlocks proves a minimal project keeps a minimal file:
// Save does not emit empty [setup]/[ports]/[verify] blocks.
func TestSaveOmitsUnconfiguredBlocks(t *testing.T) {
	dir := t.TempDir()
	body := `
[project]
agent_priority = ["claude"]
`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if err := Save(loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range []string{"[setup]", "[ports]", "[verify]"} {
		if got := string(out); strings.Contains(got, block) {
			t.Errorf("Save emitted %s for unconfigured project:\n%s", block, got)
		}
	}
}
