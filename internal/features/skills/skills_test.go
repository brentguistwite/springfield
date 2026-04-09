package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/playbooks"
)

func TestCatalogShapeLockedToPlanAndExplain(t *testing.T) {
	catalog := Catalog()
	if len(catalog) != 2 {
		t.Fatalf("catalog len = %d, want 2", len(catalog))
	}

	got := []string{catalog[0].Name, catalog[1].Name}
	want := []string{"plan", "explain"}
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

	def, err := Lookup("explain")
	if err != nil {
		t.Fatalf("lookup explain: %v", err)
	}

	rendered, err := Render(root, def.Name)
	if err != nil {
		t.Fatalf("render explain: %v", err)
	}

	out, err := playbooks.Build(playbooks.Input{
		Purpose:     def.Purpose,
		ProjectRoot: root,
		TaskBody:    def.TaskBody,
	})
	if err != nil {
		t.Fatalf("build explain playbook: %v", err)
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

func TestInstallWritesSelectedSkillDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "skills")

	installed, err := Install(root, target, []string{"plan"})
	if err != nil {
		t.Fatalf("install plan: %v", err)
	}

	if len(installed) != 1 {
		t.Fatalf("installed len = %d, want 1", len(installed))
	}
	if installed[0].Skill.Name != "plan" {
		t.Fatalf("installed skill = %q, want plan", installed[0].Skill.Name)
	}

	data, err := os.ReadFile(filepath.Join(target, "plan", "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed plan skill: %v", err)
	}
	if !strings.Contains(string(data), "Springfield") {
		t.Fatalf("expected installed plan wrapper to mention Springfield, got:\n%s", string(data))
	}
}

func TestCatalogUsesSpringfieldOwnedPurposes(t *testing.T) {
	catalog := Catalog()

	if got, want := catalog[0].Purpose, playbooks.PurposePlan; got != want {
		t.Fatalf("catalog[0] purpose = %q, want %q", got, want)
	}
	if got, want := catalog[1].Purpose, playbooks.PurposeExplain; got != want {
		t.Fatalf("catalog[1] purpose = %q, want %q", got, want)
	}
}
