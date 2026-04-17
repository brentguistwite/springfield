package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHelpTargetsClaudeCodeAndCodex(t *testing.T) {
	output, err := runSpringfield(t, "install", "--help")
	if err != nil {
		t.Fatalf("install --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Sync Springfield local host artifacts for Claude Code and Codex.",
		"local bootstrap",
		"fallback workflows",
		"claude-code",
		"codex",
	} {
		if !strings.Contains(strings.ToLower(output), strings.ToLower(marker)) {
			t.Fatalf("expected install help to contain %q, got:\n%s", marker, output)
		}
	}
	for _, stale := range []string{
		"optional",
		"wrapper",
		"skills list",
	} {
		if strings.Contains(strings.ToLower(output), stale) {
			t.Fatalf("expected install help to omit %q, got:\n%s", stale, output)
		}
	}
}

func TestInstallWritesClaudeAndCodexArtifacts(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude", "commands")
	codexDir := filepath.Join(dir, ".codex", "skills")
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("project context from AGENTS"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	output, err := runBinaryIn(
		t,
		bin,
		dir,
		"install",
		"--claude-dir", claudeDir,
		"--codex-dir", codexDir,
	)
	if err != nil {
		t.Fatalf("springfield install failed: %v\n%s", err, output)
	}

	claudePath := filepath.Join(claudeDir, "springfield.md")
	codexPath := filepath.Join(codexDir, "springfield", "SKILL.md")
	for _, marker := range []string{
		"Synced Springfield local host artifacts:",
		claudePath,
		codexPath,
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected install output to contain %q, got:\n%s", marker, output)
		}
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "claude", path: claudePath},
		{name: "codex", path: codexPath},
	} {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read installed %s artifact: %v", tc.name, err)
		}
		text := string(data)
		for _, marker := range []string{
			"Springfield",
			"project context from AGENTS",
			"If the user asks what Springfield does",
		} {
			if !strings.Contains(text, marker) {
				t.Fatalf("expected installed %s artifact to contain %q, got:\n%s", tc.name, marker, text)
			}
		}
		for _, legacy := range []string{"Ralph", "Conductor"} {
			if strings.Contains(text, legacy) {
				t.Fatalf("expected installed %s artifact to omit %q, got:\n%s", tc.name, legacy, text)
			}
		}
	}
}

func TestInstallRerunIsDeterministic(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude", "commands")
	codexDir := filepath.Join(dir, ".codex", "skills")

	output1, err := runBinaryIn(
		t,
		bin,
		dir,
		"install",
		"--claude-dir", claudeDir,
		"--codex-dir", codexDir,
	)
	if err != nil {
		t.Fatalf("first install failed: %v\n%s", err, output1)
	}

	output2, err := runBinaryIn(
		t,
		bin,
		dir,
		"install",
		"--claude-dir", claudeDir,
		"--codex-dir", codexDir,
	)
	if err != nil {
		t.Fatalf("second install failed: %v\n%s", err, output2)
	}

	if output1 != output2 {
		t.Fatalf("expected deterministic install output\nfirst:\n%s\nsecond:\n%s", output1, output2)
	}
}

func TestInstallSupportsSingleHostSelection(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude", "commands")
	codexDir := filepath.Join(dir, ".codex", "skills")

	output, err := runBinaryIn(
		t,
		bin,
		dir,
		"install",
		"--host", "codex",
		"--claude-dir", claudeDir,
		"--codex-dir", codexDir,
	)
	if err != nil {
		t.Fatalf("single-host install failed: %v\n%s", err, output)
	}

	if strings.Contains(output, filepath.Join(claudeDir, "springfield.md")) {
		t.Fatalf("expected codex-only install to omit claude output, got:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(claudeDir, "springfield.md")); !os.IsNotExist(err) {
		t.Fatalf("expected codex-only install to skip claude artifact, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(codexDir, "springfield", "SKILL.md")); err != nil {
		t.Fatalf("expected codex-only install to write codex artifact: %v", err)
	}
}

func TestSpringfieldInstallEmitsPlanInSpringfieldSkillsList(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "codex")

	codexDir := filepath.Join(dir, ".codex-install")
	if _, err := runBinaryIn(t, bin, dir, "install", "--codex-dir", codexDir, "--host", "codex"); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	helperPath := filepath.Join(codexDir, "springfield", "SKILL.md")
	body, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatalf("read installed codex helper %s: %v", helperPath, err)
	}
	if !strings.Contains(string(body), "## Springfield Skills") {
		t.Fatalf("installed codex helper missing 'Springfield Skills' section:\n%s", body)
	}
	if !strings.Contains(string(body), "- plan") {
		t.Fatalf("installed codex helper missing '- plan' under Springfield Skills:\n%s", body)
	}
}

func TestInstallDefaultsCodexToAgentsSkillsDir(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	home := t.TempDir()

	output, err := runBinaryInWithEnv(
		t,
		bin,
		dir,
		[]string{"HOME=" + home},
		"install",
		"--host", "codex",
	)
	if err != nil {
		t.Fatalf("default codex install failed: %v\n%s", err, output)
	}

	want := filepath.Join(home, ".agents", "skills", "springfield", "SKILL.md")
	if !strings.Contains(output, want) {
		t.Fatalf("expected output to contain default codex path %q, got:\n%s", want, output)
	}

	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read default codex skill: %v", err)
	}
	if !strings.Contains(string(data), "name: springfield") {
		t.Fatalf("expected default codex skill to include frontmatter, got:\n%s", string(data))
	}
}
