package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/playbooks"
)

func TestCatalogShapeLockedToClaudeCodeAndCodex(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 2 {
		t.Fatalf("catalog len = %d, want 2", len(catalog))
	}

	got := []string{catalog[0].Name, catalog[1].Name}
	want := []string{"claude-code", "codex"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalog[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRenderUsesSharedPlaybookPrompt(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agents context"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	def, err := Lookup("codex")
	if err != nil {
		t.Fatalf("lookup codex: %v", err)
	}

	rendered, err := Render(root, def.Name)
	if err != nil {
		t.Fatalf("render codex: %v", err)
	}

	out, err := playbooks.Build(playbooks.Input{
		Purpose:     def.Purpose,
		ProjectRoot: root,
		TaskBody:    def.TaskBody,
	})
	if err != nil {
		t.Fatalf("build codex playbook: %v", err)
	}

	if rendered.Prompt != out.Prompt {
		t.Fatalf("expected prompt to come from shared playbook builder")
	}
	for _, marker := range []string{"Springfield", "Built-in Springfield playbook.", "agents context"} {
		if !strings.Contains(rendered.Content, marker) {
			t.Fatalf("expected rendered content to contain %q, got:\n%s", marker, rendered.Content)
		}
	}
	for _, legacy := range []string{"Ralph", "Conductor"} {
		if strings.Contains(rendered.Content, legacy) {
			t.Fatalf("expected rendered content to omit %q, got:\n%s", legacy, rendered.Content)
		}
	}
}

func TestInstallWritesSelectedHostArtifacts(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude", "commands")
	codexDir := filepath.Join(root, ".codex", "skills")

	installed, err := Install(root, InstallOptions{
		Hosts:     []string{"codex"},
		ClaudeDir: claudeDir,
		CodexDir:  codexDir,
	})
	if err != nil {
		t.Fatalf("install codex: %v", err)
	}

	if len(installed) != 1 {
		t.Fatalf("installed len = %d, want 1", len(installed))
	}
	if installed[0].Host.Name != "codex" {
		t.Fatalf("installed host = %q, want codex", installed[0].Host.Name)
	}

	data, err := os.ReadFile(filepath.Join(codexDir, "springfield", "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed codex artifact: %v", err)
	}
	if !strings.Contains(string(data), "Springfield") {
		t.Fatalf("expected installed codex artifact to mention Springfield, got:\n%s", string(data))
	}
	if _, err := os.Stat(filepath.Join(claudeDir, "springfield.md")); !os.IsNotExist(err) {
		t.Fatalf("expected codex-only install to skip claude artifact, stat err=%v", err)
	}
}

func TestCatalogUsesPlanPlaybookPurpose(t *testing.T) {
	catalog := Catalog()

	for i, host := range catalog {
		if got, want := host.Purpose, playbooks.PurposePlan; got != want {
			t.Fatalf("catalog[%d] purpose = %q, want %q", i, got, want)
		}
	}
}
