package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsHelpMarksWrappersOptional(t *testing.T) {
	output, err := runSpringfield(t, "skills", "--help")
	if err != nil {
		t.Fatalf("skills --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Install or inspect optional Springfield direct skill wrappers.",
		"optional power-user wrappers",
		"list",
		"install",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected skills help to contain %q, got:\n%s", marker, output)
		}
	}
	for _, legacy := range []string{"Ralph", "Conductor"} {
		if strings.Contains(output, legacy) {
			t.Fatalf("expected skills help to stay Springfield-first, got:\n%s", output)
		}
	}
}

func TestSkillsListShowsPlanAndExplainOnly(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "skills", "list")
	if err != nil {
		t.Fatalf("springfield skills list failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Available Springfield direct skills:",
		"plan",
		"explain",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected skills list to contain %q, got:\n%s", marker, output)
		}
	}
	for _, unexpected := range []string{"run", "resume"} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("expected skills list to omit %q, got:\n%s", unexpected, output)
		}
	}
}

func TestSkillsInstallWritesWrapperDirectories(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "installed-skills")
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("project context from AGENTS"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "skills", "install", "--dir", skillsDir)
	if err != nil {
		t.Fatalf("springfield skills install failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Installed Springfield skill wrappers:",
		filepath.Join(skillsDir, "plan", "SKILL.md"),
		filepath.Join(skillsDir, "explain", "SKILL.md"),
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected install output to contain %q, got:\n%s", marker, output)
		}
	}

	for _, name := range []string{"plan", "explain"} {
		path := filepath.Join(skillsDir, name, "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read installed skill %s: %v", name, err)
		}
		text := string(data)
		if !strings.Contains(text, "Springfield") {
			t.Fatalf("expected installed %s skill to mention Springfield, got:\n%s", name, text)
		}
		if !strings.Contains(text, "project context from AGENTS") {
			t.Fatalf("expected installed %s skill to include AGENTS context, got:\n%s", name, text)
		}
	}
}

func TestSkillsInstallRequiresDir(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()

	output, err := runBinaryIn(t, bin, dir, "skills", "install")
	if err == nil {
		t.Fatalf("expected skills install without --dir to fail, got:\n%s", output)
	}
	if !strings.Contains(output, "--dir is required") {
		t.Fatalf("expected missing dir error, got:\n%s", output)
	}
}
