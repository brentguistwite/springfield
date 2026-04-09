package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
)

func conductorDebugArgs(args ...string) []string {
	return append([]string{"internal-debug", "conductor"}, args...)
}

func TestConductorSetupAppearsInHelp(t *testing.T) {
	output, err := runSpringfield(t, "internal-debug", "conductor", "--help")
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

	output, err := runBinaryIn(t, bin, dir, conductorDebugArgs("setup")...)
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Created") {
		t.Errorf("expected Created message, got:\n%s", output)
	}

	// Config file must exist
	configPath := filepath.Join(dir, ".springfield", "execution", "config.json")
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("config file not created at %s: %v", configPath, err)
	}
}

func TestConductorSetupDefaultsToLocalPlanStorage(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	output, err := runBinaryInWithInput(t, bin, dir, "\n", conductorDebugArgs("setup")...)
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	configPath := filepath.Join(dir, ".springfield", "execution", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg conductor.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if cfg.PlansDir != ".springfield/execution/plans" {
		t.Fatalf("PlansDir = %q, want local default", cfg.PlansDir)
	}

	if strings.Contains(output, ".gitignore") {
		t.Fatalf("local default should not prompt about .gitignore, got:\n%s", output)
	}
}

func TestConductorSetupTrackedModeOffersGitignoreUpdate(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	output, err := runBinaryInWithInput(t, bin, dir, "tracked\n", conductorDebugArgs("setup")...)
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	configPath := filepath.Join(dir, ".springfield", "execution", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg conductor.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if cfg.PlansDir != "springfield/plans" {
		t.Fatalf("PlansDir = %q, want tracked path", cfg.PlansDir)
	}

	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err == nil {
		t.Fatal("did not expect tracked setup to write .gitignore")
	}
	if strings.Contains(output, ".gitignore") {
		t.Fatalf("did not expect .gitignore output for tracked setup, got:\n%s", output)
	}
}

func TestConductorSetupTrackedModeCanSkipGitignoreUpdate(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	output, err := runBinaryInWithInput(t, bin, dir, "tracked\n", conductorDebugArgs("setup")...)
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err == nil {
		t.Fatalf("did not expect .gitignore to be created when update declined")
	}

	if strings.Contains(output, ".gitignore") {
		t.Fatalf("did not expect .gitignore prompt or snippet, got:\n%s", output)
	}
}

func TestConductorSetupFailsWithoutInit(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, conductorDebugArgs("setup")...)
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
	if _, err := runBinaryIn(t, bin, dir, conductorDebugArgs("setup")...); err != nil {
		t.Fatalf("first setup failed: %v", err)
	}

	// Second setup should reuse
	output, err := runBinaryIn(t, bin, dir, conductorDebugArgs("setup")...)
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

	output, err := runBinaryIn(t, bin, dir, conductorDebugArgs("setup")...)
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

	output, err := runBinaryIn(t, bin, dir, conductorDebugArgs("setup", "--tool", "codex")...)
	if err != nil {
		t.Fatalf("conductor setup --tool codex failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Codex CLI") {
		t.Errorf("expected Codex agent guidance, got:\n%s", output)
	}
}

func TestConductorSetupUsesEffectivePriorityHead(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	config := strings.Join([]string{
		"[project]",
		`default_agent = "claude"`,
		`agent_priority = ["gemini", "codex", "claude"]`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "springfield.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write springfield.toml: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, conductorDebugArgs("setup")...)
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".springfield", "execution", "config.json"))
	if err != nil {
		t.Fatalf("read conductor config: %v", err)
	}

	var cfg conductor.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal conductor config: %v", err)
	}

	if cfg.Tool != "gemini" {
		t.Fatalf("tool = %q, want gemini", cfg.Tool)
	}

	if cfg.FallbackTool != "codex" {
		t.Fatalf("fallback_tool = %q, want codex", cfg.FallbackTool)
	}

	if !strings.Contains(output, `"gemini" CLI`) {
		t.Fatalf("expected Gemini guidance in output, got:\n%s", output)
	}

	if strings.Contains(output, "Claude Code CLI") {
		t.Fatalf("expected setup output to prefer Gemini guidance, got:\n%s", output)
	}
}

func TestInitHintsAtGuidedSetup(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init")
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, `Next: run "springfield" to continue in guided setup.`) {
		t.Errorf("expected init to hint at guided setup, got:\n%s", output)
	}
}

func TestConductorSetupShowsNextSteps(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, conductorDebugArgs("setup")...)
	if err != nil {
		t.Fatalf("conductor setup failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Next steps") {
		t.Errorf("expected next steps in output, got:\n%s", output)
	}

	if !strings.Contains(output, "springfield internal-debug conductor run") {
		t.Errorf("expected conductor run hint, got:\n%s", output)
	}
}

func TestConductorRunFromNestedDirUsesResolvedProjectRoot(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	cfg := &conductor.Config{
		PlansDir:   ".springfield/execution/plans",
		Tool:       "bogus",
		Sequential: []string{"01-bootstrap"},
	}
	writeConductorConfigBinary(t, dir, cfg)
	writePlanFileBinary(t, dir, ".springfield/conductor/plans", "01-bootstrap", "implement bootstrap")

	nestedDir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}

	output, err := runBinaryInWithEnv(
		t,
		bin,
		dir,
		[]string{"PATH=" + t.TempDir()},
		conductorDebugArgs("run", "--dir", nestedDir)...,
	)
	if err == nil {
		t.Fatalf("expected conductor run to fail, output:\n%s", output)
	}

	nestedPlanPath := filepath.Join(nestedDir, ".springfield", "execution", "plans")
	if strings.Contains(output, nestedPlanPath) {
		t.Fatalf("expected plan lookup to avoid nested path %q, got:\n%s", nestedPlanPath, output)
	}

	if !strings.Contains(output, "01-bootstrap") {
		t.Fatalf("expected output to mention attempted plan, got:\n%s", output)
	}
}
