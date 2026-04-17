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

// TestInitGeminiRejected verifies gemini is rejected with exit code != 0.
func TestInitGeminiRejected(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "gemini")
	if err == nil {
		t.Fatalf("expected error for --agents gemini, got success:\n%s", output)
	}
	if !strings.Contains(output, "gemini is not yet supported for execution") {
		t.Errorf("expected rejection message, got:\n%s", output)
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

// TestInitReInitPrintsBackupPath verifies re-init prints the backup path.
func TestInitReInitPrintsBackupPath(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	// First init
	_, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex")
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Second init (re-init)
	output, err := runBinaryIn(t, bin, dir, "init", "--agents", "codex,claude")
	if err != nil {
		t.Fatalf("re-init failed: %v\n%s", err, output)
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
