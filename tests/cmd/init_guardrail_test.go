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

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
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

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
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

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
		t.Fatalf("init 1: %v", err)
	}
	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
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

// TestInitGuardrailPreservesExistingContent verifies pre-existing content in
// CLAUDE.md is preserved and the guardrail lands appended at the end.
func TestInitGuardrailPreservesExistingContent(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	existing := "# My Project\n\nImportant project-specific notes.\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := runBinaryIn(t, bin, dir, "init"); err != nil {
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
