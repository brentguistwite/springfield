package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConductorSetupAppearsInHelp(t *testing.T) {
	output, err := runSpringfield(t, "conductor", "--help")
	if err != nil {
		t.Fatalf("conductor --help failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "setup") {
		t.Fatalf("expected conductor help to mention setup, got:\n%s", output)
	}
}

func TestConductorSetupGeneratesConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	// Init project first
	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "conductor", "setup")
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Created") {
		t.Errorf("expected Created message, got:\n%s", output)
	}

	// Config file must exist
	configPath := filepath.Join(dir, ".springfield", "conductor", "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("config file not created at %s: %v", configPath, err)
	}
}

func TestConductorSetupFailsWithoutInit(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "conductor", "setup")
	if err == nil {
		t.Fatalf("expected conductor setup to fail without init, got:\n%s", output)
	}

	if !strings.Contains(output, "springfield.toml") {
		t.Errorf("expected missing config error, got:\n%s", output)
	}
}

func TestConductorSetupReusesExistingConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// First setup
	if _, err := runBinaryIn(t, bin, dir, "conductor", "setup"); err != nil {
		t.Fatalf("first setup failed: %v", err)
	}

	// Second setup should reuse
	output, err := runBinaryIn(t, bin, dir, "conductor", "setup")
	if err != nil {
		t.Fatalf("second setup failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "reusing") {
		t.Errorf("expected reuse message on second run, got:\n%s", output)
	}
}

func TestConductorSetupShowsAgentGuidance(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "conductor", "setup")
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	// Default agent is claude — should show install/auth guidance
	if !strings.Contains(output, "Claude Code CLI") {
		t.Errorf("expected Claude agent guidance, got:\n%s", output)
	}

	if !strings.Contains(output, "install") || !strings.Contains(output, "Auth") {
		t.Errorf("expected install and auth guidance, got:\n%s", output)
	}
}

func TestConductorSetupWithToolFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "conductor", "setup", "--tool", "codex")
	if err != nil {
		t.Fatalf("conductor setup --tool codex failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Codex CLI") {
		t.Errorf("expected Codex agent guidance, got:\n%s", output)
	}
}

func TestInitHintsAtConductorSetup(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init")
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "conductor setup") {
		t.Errorf("expected init to hint at conductor setup, got:\n%s", output)
	}
}

func TestConductorSetupShowsNextSteps(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "conductor", "setup")
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Next steps") {
		t.Errorf("expected next steps in output, got:\n%s", output)
	}

	if !strings.Contains(output, "springfield conductor run") {
		t.Errorf("expected conductor run hint, got:\n%s", output)
	}
}
