package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestInitBootstrapsExecutionConfig pins the first-run contract that `init`
// must leave the project in a loadable state: .springfield/execution/config.json
// exists and is seeded from springfield.toml's primary agent. Before this was
// fixed, init created only springfield.toml + an empty .springfield/, so every
// read path (plan --dry-run, status, plans list) failed on a missing config.json
// until a mutating command lazily bootstrapped it.
func TestInitBootstrapsExecutionConfig(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	cfgPath := filepath.Join(dir, ".springfield", "execution", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("init did not bootstrap execution config: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("execution config is not valid JSON: %v\n%s", err, data)
	}
	if cfg["tool"] != "claude" {
		t.Errorf("execution config tool = %v, want claude (first agent in priority)", cfg["tool"])
	}
	if cfg["plans_dir"] == nil || cfg["plans_dir"] == "" {
		t.Errorf("execution config missing plans_dir:\n%s", data)
	}
}

// TestInitReInitSyncsExecutionToolPreservingPlans verifies that re-initializing
// with a different primary agent updates config.json's tool to match, while
// leaving the registered plan registry intact. EnsureExecutionConfig reuses the
// existing config unchanged, so without the explicit tool sync the tool field
// would stay stale after an agent-priority change.
func TestInitReInitSyncsExecutionToolPreservingPlans(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude,codex"); err != nil {
		t.Fatalf("first init failed: %v\n%s", err, out)
	}

	// Register a plan so we can prove the sync does not wipe the registry.
	planFile := filepath.Join(dir, ".springfield", "plans", "feature.md")
	if err := os.MkdirAll(filepath.Dir(planFile), 0o755); err != nil {
		t.Fatalf("mkdir plans dir: %v", err)
	}
	if err := os.WriteFile(planFile, []byte("# Feature\n"), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
	if out, err := runBinaryIn(t, bin, dir, "plans", "add", "--id", "feat-a", "--path", "feature.md"); err != nil {
		t.Fatalf("plans add failed: %v\n%s", err, out)
	}

	cfgPath := filepath.Join(dir, ".springfield", "execution", "config.json")
	readTool := func() (string, map[string]any) {
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("decode config: %v\n%s", err, data)
		}
		tool, _ := cfg["tool"].(string)
		return tool, cfg
	}

	if tool, _ := readTool(); tool != "claude" {
		t.Fatalf("after first init, tool = %q, want claude", tool)
	}

	// Re-init with codex first.
	if out, err := runBinaryIn(t, bin, dir, "init", "--agents", "codex,claude"); err != nil {
		t.Fatalf("re-init failed: %v\n%s", err, out)
	}

	tool, cfg := readTool()
	if tool != "codex" {
		t.Errorf("after re-init, tool = %q, want codex (synced to new primary)", tool)
	}
	units, ok := cfg["plan_units"].([]any)
	if !ok || len(units) != 1 {
		t.Fatalf("re-init must preserve registered plan_units, got: %v", cfg["plan_units"])
	}
}

// TestInitThenPlanDryRunSucceeds is the regression test for the reported bug:
// `plan --prd - --dry-run` immediately after a real `init` must succeed instead
// of crashing on a missing execution/config.json.
func TestInitThenPlanDryRunSucceeds(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	pre := fingerprintSpringfieldDir(t, dir)

	env := buildEnvelopeJSON(t, minEnvelope("preview-01", "Preview 01"))
	out, err := planWithPRD(t, bin, dir, env, "--dry-run")
	if err != nil {
		t.Fatalf("plan --dry-run after init should succeed, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Dry run: would compile batch") {
		t.Fatalf("expected dry-run summary:\n%s", out)
	}

	post := fingerprintSpringfieldDir(t, dir)
	if diff, eq := mapEqual(pre, post); !eq {
		t.Fatalf(".springfield/ mutated by --dry-run: %s", diff)
	}
}

// TestInitThenStatusReportsEmptyRegistry verifies that status after a real init
// reports an empty-but-valid registry and signposts the plan skill — matching
// init's own Next: copy — rather than the stale "No Springfield execution
// config" / bare "plans add" guidance that contradicted it.
func TestInitThenStatusReportsEmptyRegistry(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	if out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	out, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status after init failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "No Springfield execution config") {
		t.Errorf("status should not claim missing execution config after init:\n%s", out)
	}
	if !strings.Contains(out, "/springfield:plan") {
		t.Errorf("status empty-registry next-step should point at the plan skill:\n%s", out)
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
// with an empty stdin still fails before writing springfield.toml. The
// init form pre-selects claude as the default priority (the lead agent; see
// agents.ClaudeHeadlessMetered), but the model picker and write confirmation
// still require input — empty stdin cannot satisfy them.
func TestInitNonTTYEmptyStdinErrors(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryInWithInput(t, bin, dir, "", "init")
	if err == nil {
		t.Fatalf("expected error when non-interactive with empty stdin, output:\n%s", output)
	}

	// No springfield.toml should have been written.
	if _, statErr := os.Stat(filepath.Join(dir, "springfield.toml")); statErr == nil {
		t.Fatalf("expected no springfield.toml on error path")
	}
}

// TestInitNonTTYPipedAccessibleModeMatchesFlagOutput pipes the canonical
// "claude only, adapter default" answer script into init's accessible-mode
// form and asserts the resulting springfield.toml is byte-identical to the
// flag-driven equivalent. Claude is the lead/default agent while
// agents.ClaudeHeadlessMetered is false (Anthropic reverted the 2026-05-14
// `claude -p` metering change).
//
// Answer-script derivation (empirical, 2026-05-15):
//
//	Prompt                                             Input     Effect
//	─────────────────────────────────────────────────  ──────    ───────────────────────────
//	MultiSelect "Which agents..." (claude pre-checked) "0\n"     confirm selection as-is
//	Select "Model for claude"                          "1\n"     pick "(use adapter default)"
//	Confirm "Write springfield.toml..."                "y\n"     write
//
// Drift warning: huh's accessible-mode output format (numbered separators,
// prompt phrasing) is not API-stable. If this test fails on a `huh` bump:
//  1. Run `printf ” | springfield init` interactively against a temp dir.
//  2. Re-derive the answer script from observed prompts.
//  3. Update this fixture in a chore(deps) commit alongside the bump.
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

	const cannedAnswers = "0\n1\ny\n"
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

// TestInitBranchModeFlagsWriteProjectKeys verifies --branch-mode and
// --base-branch land in [project] of springfield.toml.
func TestInitBranchModeFlagsWriteProjectKeys(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init",
		"--agents", "claude",
		"--branch-mode", "per-plan",
		"--base-branch", "main",
	)
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, output)
	}

	content, err := os.ReadFile(filepath.Join(dir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read springfield.toml: %v", err)
	}
	toml := string(content)
	if !strings.Contains(toml, `branch_mode = "per-plan"`) {
		t.Errorf("expected branch_mode in config:\n%s", toml)
	}
	if !strings.Contains(toml, `base_branch = "main"`) {
		t.Errorf("expected base_branch in config:\n%s", toml)
	}
}

// TestInitTwiceBranchModeFlagsNotDuplicated is the flags half of the US-007
// idempotency pin: re-running init with --branch-mode/--base-branch must leave
// exactly one branch_mode and one base_branch key in [project]. Re-init loads
// springfield.toml into a struct and re-marshals it, so duplicate keys are
// structurally impossible today — this locks that "re-serialize, don't append"
// contract at the binary boundary so a future string-append config writer can't
// silently regress it.
func TestInitTwiceBranchModeFlagsNotDuplicated(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	args := []string{"init", "--agents", "claude", "--branch-mode", "per-plan", "--base-branch", "main"}
	if out, err := runBinaryIn(t, bin, dir, args...); err != nil {
		t.Fatalf("first init: %v\n%s", err, out)
	}
	if out, err := runBinaryIn(t, bin, dir, args...); err != nil {
		t.Fatalf("second init: %v\n%s", err, out)
	}

	content, err := os.ReadFile(filepath.Join(dir, "springfield.toml"))
	if err != nil {
		t.Fatalf("read springfield.toml: %v", err)
	}
	toml := string(content)
	for _, key := range []string{"branch_mode =", "base_branch ="} {
		if n := strings.Count(toml, key); n != 1 {
			t.Errorf("key %q appears %d times after re-init, want exactly 1; config:\n%s", key, n, toml)
		}
	}
}

// TestInitInvalidBranchModeExitsNonZero verifies an out-of-range --branch-mode
// aborts init with a clear message and a non-zero exit, leaving no config.
func TestInitInvalidBranchModeExitsNonZero(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "init",
		"--agents", "claude",
		"--branch-mode", "bogus",
	)
	if err == nil {
		t.Fatalf("expected non-zero exit, output:\n%s", output)
	}
	if !strings.Contains(output, "invalid --branch-mode") {
		t.Errorf("expected clear branch-mode error, got:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "springfield.toml")); statErr == nil {
		t.Error("invalid init should not have written springfield.toml")
	}
}
