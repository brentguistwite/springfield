package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Canonical-AGENTS behavior:
// - AGENTS.md is the canonical source of truth on a greenfield init.
// - Selected agents' filenames (CLAUDE.md for claude, GEMINI.md for gemini)
//   are created as relative symlinks to AGENTS.md when missing. Codex's
//   native filename IS AGENTS.md, so no extra symlink is needed.
// - When the operator already has CLAUDE.md or GEMINI.md as the canonical
//   real file (and AGENTS.md absent), init respects that — does not force
//   AGENTS.md to be canonical, but creates AGENTS.md as a symlink to the
//   existing real file when codex is selected.

func TestInitGreenfieldClaudeOnlyCreatesAgentsMdAndClaudeSymlink(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude"); err != nil {
		t.Fatalf("init: %v", err)
	}

	agents := filepath.Join(dir, "AGENTS.md")
	claude := filepath.Join(dir, "CLAUDE.md")
	gemini := filepath.Join(dir, "GEMINI.md")

	infoAgents, err := os.Lstat(agents)
	if err != nil {
		t.Fatalf("lstat AGENTS.md: %v", err)
	}
	if !infoAgents.Mode().IsRegular() {
		t.Errorf("AGENTS.md not a regular file: mode=%v", infoAgents.Mode())
	}

	infoClaude, err := os.Lstat(claude)
	if err != nil {
		t.Fatalf("lstat CLAUDE.md: %v", err)
	}
	if infoClaude.Mode()&os.ModeSymlink == 0 {
		t.Errorf("CLAUDE.md not a symlink: mode=%v", infoClaude.Mode())
	}
	target, err := os.Readlink(claude)
	if err != nil {
		t.Fatalf("readlink CLAUDE.md: %v", err)
	}
	if target != "AGENTS.md" {
		t.Errorf("CLAUDE.md symlink target = %q, want AGENTS.md", target)
	}

	if _, err := os.Lstat(gemini); !os.IsNotExist(err) {
		t.Errorf("GEMINI.md should not exist when only claude selected, got err=%v", err)
	}

	// Reading via either path returns canonical content with marker.
	via, err := os.ReadFile(claude)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(via), guardrailMarker) {
		t.Errorf("CLAUDE.md content (via symlink) missing guardrail")
	}
}

func TestInitGreenfieldCodexOnlyCreatesAgentsMdNoSymlinks(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	agents := filepath.Join(dir, "AGENTS.md")
	infoAgents, err := os.Lstat(agents)
	if err != nil {
		t.Fatalf("lstat AGENTS.md: %v", err)
	}
	if !infoAgents.Mode().IsRegular() {
		t.Errorf("AGENTS.md not a regular file: mode=%v", infoAgents.Mode())
	}

	if _, err := os.Lstat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md should not exist when only codex selected, got err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "GEMINI.md")); !os.IsNotExist(err) {
		t.Errorf("GEMINI.md should not exist when only codex selected, got err=%v", err)
	}

	data, err := os.ReadFile(agents)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(data), guardrailMarker) {
		t.Errorf("AGENTS.md missing guardrail marker")
	}
}

func TestInitGreenfieldAllAgentsCreatesAgentsMdPlusSymlinks(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex,gemini"); err != nil {
		t.Fatalf("init: %v", err)
	}

	agents := filepath.Join(dir, "AGENTS.md")
	infoAgents, err := os.Lstat(agents)
	if err != nil {
		t.Fatalf("lstat AGENTS.md: %v", err)
	}
	if !infoAgents.Mode().IsRegular() {
		t.Errorf("AGENTS.md not a regular file: mode=%v", infoAgents.Mode())
	}

	for _, name := range []string{"CLAUDE.md", "GEMINI.md"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s not a symlink: mode=%v", name, info.Mode())
		}
		target, err := os.Readlink(path)
		if err != nil {
			t.Fatalf("readlink %s: %v", name, err)
		}
		if target != "AGENTS.md" {
			t.Errorf("%s symlink target = %q, want AGENTS.md", name, target)
		}
	}
}

func TestInitRespectsExistingClaudeMdAsCanonicalWhenClaudeOnly(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	claudePath := filepath.Join(dir, "CLAUDE.md")
	existing := "# My Project\n\nProject notes.\n"
	if err := os.WriteFile(claudePath, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// CLAUDE.md remains the canonical real file with existing content + guardrail.
	info, err := os.Lstat(claudePath)
	if err != nil {
		t.Fatalf("lstat CLAUDE.md: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("CLAUDE.md unexpectedly not a regular file: mode=%v", info.Mode())
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(data), "Project notes.") {
		t.Errorf("pre-existing content lost")
	}
	if !strings.Contains(string(data), guardrailMarker) {
		t.Errorf("CLAUDE.md missing guardrail")
	}

	// AGENTS.md should NOT be created when only claude is selected and the
	// operator already has CLAUDE.md as their canonical.
	if _, err := os.Lstat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("AGENTS.md should not exist (claude-only, CLAUDE.md canonical), got err=%v", err)
	}
}

func TestInitCreatesAgentsMdSymlinkWhenCodexJoinsExistingClaudeMd(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	claudePath := filepath.Join(dir, "CLAUDE.md")
	existing := "# My Project\n\nProject notes.\n"
	if err := os.WriteFile(claudePath, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed CLAUDE.md: %v", err)
	}

	// Add codex to the agent list — AGENTS.md must now exist (codex's native file).
	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init: %v", err)
	}

	// CLAUDE.md remains canonical real file.
	infoClaude, err := os.Lstat(claudePath)
	if err != nil {
		t.Fatalf("lstat CLAUDE.md: %v", err)
	}
	if !infoClaude.Mode().IsRegular() {
		t.Errorf("CLAUDE.md should remain a real file, got mode=%v", infoClaude.Mode())
	}

	// AGENTS.md must be a symlink to CLAUDE.md.
	agentsPath := filepath.Join(dir, "AGENTS.md")
	infoAgents, err := os.Lstat(agentsPath)
	if err != nil {
		t.Fatalf("lstat AGENTS.md: %v", err)
	}
	if infoAgents.Mode()&os.ModeSymlink == 0 {
		t.Errorf("AGENTS.md should be a symlink, got mode=%v", infoAgents.Mode())
	}
	target, err := os.Readlink(agentsPath)
	if err != nil {
		t.Fatalf("readlink AGENTS.md: %v", err)
	}
	if target != "CLAUDE.md" {
		t.Errorf("AGENTS.md symlink target = %q, want CLAUDE.md", target)
	}

	// Both paths read the same content with marker once.
	dataClaude, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	dataAgents, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(dataClaude) != string(dataAgents) {
		t.Errorf("CLAUDE.md and AGENTS.md content diverged")
	}
	if got := strings.Count(string(dataClaude), guardrailMarker); got != 1 {
		t.Errorf("guardrail marker count = %d, want 1", got)
	}
}

func TestInitDoesNotCreateClaudeMdSymlinkWhenClaudeNotSelected(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "codex,gemini"); err != nil {
		t.Fatalf("init: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md should not exist (claude not selected), got err=%v", err)
	}

	// AGENTS.md (canonical) and GEMINI.md (symlink) must exist.
	infoAgents, err := os.Lstat(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("lstat AGENTS.md: %v", err)
	}
	if !infoAgents.Mode().IsRegular() {
		t.Errorf("AGENTS.md not a regular file: mode=%v", infoAgents.Mode())
	}
	infoGemini, err := os.Lstat(filepath.Join(dir, "GEMINI.md"))
	if err != nil {
		t.Fatalf("lstat GEMINI.md: %v", err)
	}
	if infoGemini.Mode()&os.ModeSymlink == 0 {
		t.Errorf("GEMINI.md not a symlink: mode=%v", infoGemini.Mode())
	}
}
