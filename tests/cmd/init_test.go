package cmd_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestInitAgentsFlagSetsDefaultAgent verifies --agents flag controls default_agent.
func TestInitAgentsFlagSetsDefaultAgent(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "codex,claude")
	if err != nil {
		t.Fatalf("init --agents codex,claude failed: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(dir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read springfield.toml: %v", err)
	}
	toml := string(content)

	if !strings.Contains(toml, `default_agent = "codex"`) {
		t.Errorf("expected default_agent=codex in config:\n%s", toml)
	}
	if !strings.Contains(toml, `agent_priority = ["codex", "claude"]`) {
		t.Errorf("expected agent_priority=[codex,claude] in config:\n%s", toml)
	}
	// Both agent sections should always be present.
	if !strings.Contains(toml, "[agents.claude]") {
		t.Errorf("expected [agents.claude] section in config:\n%s", toml)
	}
	if !strings.Contains(toml, "[agents.codex]") {
		t.Errorf("expected [agents.codex] section in config:\n%s", toml)
	}
}

// TestInitAcceptsGeminiInAgentsFlag verifies gemini is accepted when passed
// via --agents (execution support flipped on in 2026-04).
func TestInitAcceptsGeminiInAgentsFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "gemini")
	if err != nil {
		t.Fatalf("init --agents gemini failed: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(dir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read springfield.toml: %v", err)
	}
	toml := string(content)
	if !strings.Contains(toml, `default_agent = "gemini"`) {
		t.Errorf("expected default_agent=gemini in config:\n%s", toml)
	}
	if !strings.Contains(toml, `agent_priority = ["gemini"]`) {
		t.Errorf("expected agent_priority=[gemini], got:\n%s", toml)
	}
	if !strings.Contains(toml, "[agents.gemini]") {
		t.Errorf("expected [agents.gemini] section:\n%s", toml)
	}
}

// TestInitNonTTYDefaultPriorityExcludesGemini locks the roadmap rule:
// without --agents, Gemini is NOT auto-added to priority even though it
// is execution-supported.
func TestInitNonTTYDefaultPriorityExcludesGemini(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryInWithInput(t, bin, dir, "", "init")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(dir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read springfield.toml: %v", err)
	}
	toml := string(content)
	if strings.Contains(toml, "gemini") {
		t.Fatalf("expected default init to exclude gemini, got:\n%s", toml)
	}
}

// TestInitNonTTYDefaultsToSupportedAgents verifies non-TTY + no flag uses SupportedForExecution.
func TestInitNonTTYDefaultsToSupportedAgents(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	// Pipe empty stdin → non-TTY, no --agents flag → should use default [claude, codex].
	output, err := runBinaryInWithInput(t, bin, dir, "", "init")
	if err != nil {
		t.Fatalf("init with empty stdin failed: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(dir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read springfield.toml: %v", err)
	}
	toml := string(content)

	if !strings.Contains(toml, `default_agent = "claude"`) {
		t.Errorf("expected default_agent=claude in config:\n%s", toml)
	}
	if !strings.Contains(toml, `agent_priority = ["claude", "codex"]`) {
		t.Errorf("expected agent_priority=[claude,codex] in config:\n%s", toml)
	}
}

// TestInitReInitNoResetNoBackup verifies that re-running init without --reset does not
// create a backup file and prints no backup message.
func TestInitReInitNoResetNoBackup(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	// First init.
	_, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex")
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Second init without --reset.
	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "codex,claude")
	if err != nil {
		t.Fatalf("re-init (no --reset) failed: %v\n%s", err, output)
	}

	if strings.Contains(output, "Backed up") {
		t.Errorf("expected no backup message on re-init without --reset, got:\n%s", output)
	}

	// Verify no .bak-* file exists.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	pattern := regexp.MustCompile(`^springfield\.toml\.bak-`)
	for _, e := range entries {
		if pattern.MatchString(e.Name()) {
			t.Errorf("unexpected backup file found: %s", e.Name())
		}
	}
}

// TestInitResetPrintsBackupPath verifies --reset prints the backup path and creates
// a .bak-* file.
func TestInitResetPrintsBackupPath(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	// First init.
	_, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex")
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Re-init with --reset.
	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "codex,claude", "--reset")
	if err != nil {
		t.Fatalf("re-init --reset failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Backed up previous springfield.toml to") {
		t.Errorf("expected backup message in output:\n%s", output)
	}

	// Verify backup file exists with expected name pattern.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	pattern := regexp.MustCompile(`^springfield\.toml\.bak-\d{8}T\d{6}Z$`)
	found := false
	for _, e := range entries {
		if pattern.MatchString(e.Name()) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no backup file found in %s", dir)
	}
}
