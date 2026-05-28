package cmd_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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

func TestInitModelFlagWritesPerAgentModels(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(
		t,
		bin,
		dir,
		"init",
		"--agents",
		"claude,codex",
		"--model",
		"claude=claude-sonnet-4-6,codex=custom-codex-model",
	)
	if err != nil {
		t.Fatalf("init --model failed: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(dir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read springfield.toml: %v", err)
	}
	toml := string(content)

	if !strings.Contains(toml, `[agents.claude]`) || !strings.Contains(toml, `model = "claude-sonnet-4-6"`) {
		t.Fatalf("expected claude model in config:\n%s", toml)
	}
	if !strings.Contains(toml, `[agents.codex]`) || !strings.Contains(toml, `model = "custom-codex-model"`) {
		t.Fatalf("expected codex model in config:\n%s", toml)
	}
}

func TestInitModelFlagRejectsAgentOutsidePriority(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(
		t,
		bin,
		dir,
		"init",
		"--agents",
		"claude",
		"--model",
		"codex=custom-codex-model",
	)
	if err == nil {
		t.Fatalf("expected init to fail, output:\n%s", output)
	}
	if !strings.Contains(output, "--model agent \"codex\" not present in --agents priority") {
		t.Fatalf("expected clear --model mismatch error, got:\n%s", output)
	}
}

func TestInitModelFlagRejectsNoUsableEntries(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(
		t,
		bin,
		dir,
		"init",
		"--agents",
		"claude",
		"--model",
		" , ",
	)
	if err == nil {
		t.Fatalf("expected init to fail, output:\n%s", output)
	}
	if !strings.Contains(output, "at least one agent=model entry is required in --model") {
		t.Fatalf("expected clear empty --model error, got:\n%s", output)
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

// TestInitNonTTYEmptyStdinErrors verifies that running init non-interactively
// without piped answers or --agents fails with a clear error. There is no
// fixed default priority — the user must opt in via flag or pipe.
func TestInitNonTTYEmptyStdinErrors(t *testing.T) {
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

// TestInitNonTTYPipedAccessibleModeMatchesFlagOutput pipes the canonical
// "claude only, adapter default" answer script into init's accessible-mode
// form and asserts the resulting springfield.toml is byte-identical to the
// flag-driven equivalent.
//
// Answer-script derivation (empirical, 2026-05-11):
//
//  Prompt                                            Input     Effect
//  ───────────────────────────────────────────────── ──────    ───────────────────────────
//  MultiSelect "Which agents..."                     "1\n"     toggle claude
//  MultiSelect (still in toggle loop)                "0\n"     confirm selection
//  Select "Model for claude"                         "1\n"     pick "(use adapter default)"
//  Confirm "Write springfield.toml..."               "y\n"     write
//
// Drift warning: huh's accessible-mode output format (numbered separators,
// prompt phrasing) is not API-stable. If this test fails on a `huh` bump:
//   1. Run `printf '' | springfield init` interactively against a temp dir.
//   2. Re-derive the answer script from observed prompts.
//   3. Update this fixture in a chore(deps) commit alongside the bump.
func TestInitNonTTYPipedAccessibleModeMatchesFlagOutput(t *testing.T) {
	bin := buildBinary(t)

	flagDir := t.TempDir()
	pipeDir := t.TempDir()

	if out, err := runBinaryIn(t, bin, flagDir, "init", "--agents", "claude"); err != nil {
		t.Fatalf("flag-driven init failed: %v\n%s", err, out)
	}
	flagBytes, err := os.ReadFile(filepath.Join(flagDir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read flag config: %v", err)
	}

	const cannedAnswers = "1\n0\n1\ny\n"
	out, err := runBinaryInWithInput(t, bin, pipeDir, cannedAnswers, "init")
	if err != nil {
		t.Fatalf("piped init failed: %v\n%s", err, out)
	}
	pipeBytes, err := os.ReadFile(filepath.Join(pipeDir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read piped config: %v", err)
	}

	if string(flagBytes) != string(pipeBytes) {
		t.Fatalf("piped accessible-mode config diverged from flag-driven:\n--- flag ---\n%s\n--- pipe ---\n%s", flagBytes, pipeBytes)
	}

	// The signpost points at the "plan" skill, not the bare `springfield plan`
	// CLI verb (which requires a compiled PRD envelope).
	if !strings.Contains(out, "/springfield:plan") {
		t.Errorf("expected post-init Next: line to point at the plan skill, got:\n%s", out)
	}
	if strings.Contains(out, "Next: springfield plan\n") {
		t.Errorf("post-init should not signpost the bare `springfield plan` verb, got:\n%s", out)
	}
}

// TestInitCreatesNoAgentInstructionFiles pins the Issue-3 removal: init must not
// create or touch AGENTS.md / CLAUDE.md / GEMINI.md, and must not append any
// guardrail block. The .springfield/ guard now lives in the plugin PreToolUse
// hook and the batch prompt header, not in the operator's agent-instruction
// files.
func TestInitCreatesNoAgentInstructionFiles(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if _, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex,gemini"); err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("init should not create %s, lstat err = %v", name, err)
		}
	}
}

// TestInitDoesNotTouchExistingAgentInstructionFiles verifies pre-existing
// AGENTS.md, CLAUDE.md, and GEMINI.md are left byte-for-byte intact — init no
// longer appends a guardrail to any of them. Table-driven so each filename is
// exercised in isolation (a single greenfield scratch dir per row).
func TestInitDoesNotTouchExistingAgentInstructionFiles(t *testing.T) {
	bin := buildBinary(t)

	cases := []struct {
		name     string
		agents   string
		filename string
	}{
		{"agents-md", "claude,codex", "AGENTS.md"},
		{"claude-md", "claude", "CLAUDE.md"},
		{"gemini-md", "gemini", "GEMINI.md"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			existing := "# My Project\n\nImportant project-specific notes.\n"
			path := filepath.Join(dir, tc.filename)
			if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
				t.Fatalf("seed %s: %v", tc.filename, err)
			}

			if _, err := runBinaryIn(t, bin, dir, "init", "--agents", tc.agents); err != nil {
				t.Fatalf("init: %v", err)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.filename, err)
			}
			if string(got) != existing {
				t.Errorf("init mutated %s; got:\n%s\nwant:\n%s", tc.filename, got, existing)
			}
		})
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
