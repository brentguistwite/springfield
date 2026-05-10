package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const guardrailMarker = "<!-- springfield:guardrail -->"
const guardrailBodySnippet = "Never read, write, edit, or delete files under `.springfield/`"

// TestInitAppendsGuardrailToClaudeMd verifies the marker + guardrail body is
// appended to CLAUDE.md on a fresh init.
func TestInitAppendsGuardrailToClaudeMd(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, guardrailMarker) {
		t.Errorf("CLAUDE.md missing guardrail marker, got:\n%s", content)
	}
	if !strings.Contains(content, guardrailBodySnippet) {
		t.Errorf("CLAUDE.md missing guardrail body, got:\n%s", content)
	}
}

// TestInitCreatesAgentsMdIfMissing verifies AGENTS.md is also populated.
func TestInitCreatesAgentsMdIfMissing(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(data), guardrailMarker) {
		t.Errorf("AGENTS.md missing guardrail marker, got:\n%s", string(data))
	}
}

// TestInitGuardrailIdempotent verifies a second init does not re-append.
func TestInitGuardrailIdempotent(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init 1: %v", err)
	}
	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init 2: %v", err)
	}

	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		count := strings.Count(string(data), guardrailMarker)
		if count != 1 {
			t.Errorf("%s has %d guardrail markers, want 1", name, count)
		}
	}
}

// TestInitGuardrailPreservesMode verifies that when CLAUDE.md exists with a
// non-default mode (e.g. 0o600), init preserves that mode after appending the
// guardrail block. Regression guard for the atomic-write pattern: naive
// os.CreateTemp + rename would otherwise land a 0o644 or 0o600-default file
// regardless of the caller's existing mode.
func TestInitGuardrailPreservesMode(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	claudePath := filepath.Join(dir, "CLAUDE.md")
	existing := "# My Project\n\nProject notes.\n"
	if err := os.WriteFile(claudePath, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}
	// os.WriteFile honours process umask; ensure mode is exactly 0o600.
	if err := os.Chmod(claudePath, 0o600); err != nil {
		t.Fatalf("chmod seed: %v", err)
	}

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	info, err := os.Stat(claudePath)
	if err != nil {
		t.Fatalf("stat CLAUDE.md: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("CLAUDE.md mode = %o, want 0600", got)
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Project notes.") {
		t.Errorf("pre-existing content lost, got:\n%s", content)
	}
	if !strings.Contains(content, guardrailMarker) {
		t.Errorf("guardrail not appended, got:\n%s", content)
	}
}

// TestInitGuardrailFreshFileDefaultMode verifies a fresh CLAUDE.md lands with
// mode 0o644 (the standard default when no existing file dictates otherwise).
func TestInitGuardrailFreshFileDefaultMode(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("stat CLAUDE.md: %v", err)
	}
	// Fresh-file baseline: 0o644 (umask may strip group/other write on some
	// systems; we tolerate the permissive case since the real regression is
	// "mode was dropped to 0o600 because tmp file carried over").
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("fresh CLAUDE.md mode = %o, want 0644", got)
	}
}

// TestInitPreservesClaudeMdSymlinkToAgents verifies that when CLAUDE.md is a
// symlink to AGENTS.md (the keep-agents-md-canonical convention), init writes
// the guardrail to the canonical target and leaves the symlink intact.
//
// Regression: previously, ensureGuardrailBlock called through
// writeFileReplacingNonRegular which detected the symlink as non-regular,
// removed it, and wrote a divergent regular file at CLAUDE.md — silently
// breaking the operator's setup.
func TestInitPreservesClaudeMdSymlinkToAgents(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	// Seed canonical AGENTS.md + symlink CLAUDE.md -> AGENTS.md.
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("# Agent Instructions\n"), 0o644); err != nil {
		t.Fatalf("seed AGENTS.md: %v", err)
	}
	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := os.Symlink("AGENTS.md", claudePath); err != nil {
		t.Fatalf("symlink CLAUDE.md: %v", err)
	}

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// CLAUDE.md must still be a symlink.
	info, err := os.Lstat(claudePath)
	if err != nil {
		t.Fatalf("lstat CLAUDE.md: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("CLAUDE.md is no longer a symlink: mode=%v", info.Mode())
	}

	// AGENTS.md must have the guardrail marker exactly once.
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	count := strings.Count(string(data), guardrailMarker)
	if count != 1 {
		t.Errorf("AGENTS.md has %d guardrail markers, want 1", count)
	}

	// Reading CLAUDE.md (follows symlink) must return the same content.
	via, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if string(via) != string(data) {
		t.Errorf("CLAUDE.md and AGENTS.md diverged after init")
	}
}

// TestInitPreservesAgentsMdSymlinkToClaude verifies the reverse-direction
// symlink (AGENTS.md -> CLAUDE.md) is also respected.
func TestInitPreservesAgentsMdSymlinkToClaude(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	claudePath := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte("# Agent Instructions\n"), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}
	agentsPath := filepath.Join(dir, "AGENTS.md")
	if err := os.Symlink("CLAUDE.md", agentsPath); err != nil {
		t.Fatalf("symlink AGENTS.md: %v", err)
	}

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	info, err := os.Lstat(agentsPath)
	if err != nil {
		t.Fatalf("lstat AGENTS.md: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("AGENTS.md is no longer a symlink: mode=%v", info.Mode())
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	count := strings.Count(string(data), guardrailMarker)
	if count != 1 {
		t.Errorf("CLAUDE.md has %d guardrail markers, want 1", count)
	}
}

// TestInitGuardrailPreservesExistingContent verifies pre-existing content in
// CLAUDE.md is preserved and the guardrail lands appended at the end.
func TestInitGuardrailPreservesExistingContent(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	existing := "# My Project\n\nImportant project-specific notes.\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Important project-specific notes.") {
		t.Errorf("pre-existing content lost, got:\n%s", content)
	}
	if !strings.Contains(content, guardrailMarker) {
		t.Errorf("guardrail not appended, got:\n%s", content)
	}
	if strings.Index(content, guardrailMarker) < strings.Index(content, "Important project-specific notes.") {
		t.Errorf("guardrail should appear after existing content, got:\n%s", content)
	}
}
