package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"springfield/cmd"
	"springfield/internal/core/agents"
)

// TestPromptShowsAllThreeAgentsWithDetection verifies the picker lists every
// execution-supported agent with a detection marker so the user can see at a
// glance which CLIs are installed before choosing.
func TestPromptShowsAllThreeAgentsWithDetection(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("claude,codex\n")
	priority, err := cmd.PromptForAgentsWithDetection(in, &out, fakeDetector{
		statuses: map[agents.ID]agents.DetectionStatus{
			agents.AgentClaude: agents.DetectionStatusAvailable,
			agents.AgentCodex:  agents.DetectionStatusMissing,
			agents.AgentGemini: agents.DetectionStatusAvailable,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"claude", "codex", "gemini", "✓", "✗"} {
		if !strings.Contains(s, want) {
			t.Fatalf("prompt output missing %q:\n%s", want, s)
		}
	}
	if !slices.Equal(priority, []string{"claude", "codex"}) {
		t.Fatalf("priority = %v, want [claude codex]", priority)
	}
}

// TestPromptRejectsAllOff verifies the picker errors out when the user
// repeatedly submits no agents — the runtime cannot proceed without at
// least one agent in the priority list.
func TestPromptRejectsAllOff(t *testing.T) {
	var out bytes.Buffer
	// strings.Repeat with maxPromptAttempts+1 newlines so the loop exhausts the cap
	// rather than hitting EOF — exercises the "too many invalid attempts" path.
	// (maxPromptAttempts = 4, so 5 newlines exceeds it)
	in := strings.NewReader(strings.Repeat("\n", 5))
	_, err := cmd.PromptForAgentsWithDetection(in, &out, fakeDetector{})
	if err == nil {
		t.Fatal("expected error when no agents selected after retries")
	}
}

// TestPromptShowsUnhealthyMarker verifies the picker renders the ⚠ marker and
// "unhealthy" descriptor when an agent's detection status is Unhealthy.
func TestPromptShowsUnhealthyMarker(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("claude\n")
	_, err := cmd.PromptForAgentsWithDetection(in, &out, fakeDetector{
		statuses: map[agents.ID]agents.DetectionStatus{
			agents.AgentClaude: agents.DetectionStatusUnhealthy,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "⚠") {
		t.Fatalf("expected unhealthy marker ⚠ in output:\n%s", s)
	}
	if !strings.Contains(s, "unhealthy") {
		t.Fatalf("expected 'unhealthy' descriptor in output:\n%s", s)
	}
}

// TestInitAgentsFlagSetsAgentPriority verifies --agents flag controls agent_priority.
func TestInitAgentsFlagSetsAgentPriority(t *testing.T) {
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

	if !strings.Contains(toml, `agent_priority = ["codex", "claude"]`) {
		t.Errorf("expected agent_priority=[codex,claude] in config:\n%s", toml)
	}
	// Both selected agent sections should be present.
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
	if !strings.Contains(toml, `agent_priority = ["gemini"]`) {
		t.Errorf("expected agent_priority=[gemini], got:\n%s", toml)
	}
	if !strings.Contains(toml, "[agents.gemini]") {
		t.Errorf("expected [agents.gemini] section:\n%s", toml)
	}
}

// TestInitNonTTYWithoutAgentsFlagErrors verifies that running init non-interactively
// (no TTY) without an explicit --agents flag fails with a clear error. There is no
// fixed default priority — the user must opt in.
func TestInitNonTTYWithoutAgentsFlagErrors(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryInWithInput(t, bin, dir, "", "init")
	if err == nil {
		t.Fatalf("expected error when non-interactive and no --agents flag, output:\n%s", output)
	}
	if !strings.Contains(output, "--agents") {
		t.Fatalf("expected error mentioning --agents, got:\n%s", output)
	}

	// No springfield.toml should have been written.
	if _, statErr := os.Stat(filepath.Join(dir, "springfield.toml")); statErr == nil {
		t.Fatalf("expected no springfield.toml on error path")
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
