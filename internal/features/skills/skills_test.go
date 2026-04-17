package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"springfield/internal/features/playbooks"
)

func TestCatalogShapeLockedToSpringfieldSkills(t *testing.T) {
	t.Parallel()

	catalog := Catalog()
	if len(catalog) != 4 {
		t.Fatalf("catalog len = %d, want 4", len(catalog))
	}

	got := []string{
		string(catalog[0].Name),
		string(catalog[1].Name),
		string(catalog[2].Name),
		string(catalog[3].Name),
	}
	want := []string{"plan", "start", "status", "recover"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("catalog[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCatalog_IncludesPlan(t *testing.T) {
	t.Parallel()

	for _, s := range Catalog() {
		if string(s.Name) == "plan" {
			if s.Purpose != playbooks.PurposePlan {
				t.Errorf("plan skill Purpose = %q, want %q", s.Purpose, playbooks.PurposePlan)
			}
			if s.RelativePath != "skills/plan/SKILL.md" {
				t.Errorf("plan skill RelativePath = %q, want skills/plan/SKILL.md", s.RelativePath)
			}
			return
		}
	}
	t.Fatalf("plan skill missing from catalog")
}

func TestLookup_Plan(t *testing.T) {
	t.Parallel()

	s, err := Lookup("plan")
	if err != nil {
		t.Fatalf("Lookup(plan): %v", err)
	}
	if string(s.Name) != "plan" {
		t.Errorf("Name = %q, want plan", s.Name)
	}
}

func TestRender_Plan(t *testing.T) {
	t.Parallel()

	r, err := Render("plan")
	if err != nil {
		t.Fatalf("Render(plan): %v", err)
	}
	if !strings.Contains(r.Content, "Springfield Plan") {
		t.Errorf("rendered content missing Springfield Plan header:\n%s", r.Content)
	}
	if !strings.Contains(r.Content, "Compile a Springfield batch") {
		t.Errorf("rendered content missing TaskBody opener:\n%s", r.Content)
	}
}

func TestRenderCommand_Plan(t *testing.T) {
	t.Parallel()

	r, err := RenderCommand("plan")
	if err != nil {
		t.Fatalf("RenderCommand(plan): %v", err)
	}
	if !strings.Contains(r.Content, "$ARGUMENTS") {
		t.Errorf("rendered command missing $ARGUMENTS hook:\n%s", r.Content)
	}
}

func TestRenderUsesSharedHostNeutralPlaybookPrompt(t *testing.T) {
	t.Parallel()

	def, err := Lookup("start")
	if err != nil {
		t.Fatalf("lookup start: %v", err)
	}

	rendered, err := Render(string(def.Name))
	if err != nil {
		t.Fatalf("render start: %v", err)
	}

	out, err := playbooks.Build(playbooks.Input{
		Purpose:               playbooks.PurposeStart,
		IncludeProjectContext: false,
		TaskBody:              def.TaskBody,
	})
	if err != nil {
		t.Fatalf("build start playbook: %v", err)
	}

	if rendered.Prompt != out.Prompt {
		t.Fatalf("expected prompt to come from shared playbook builder")
	}
	for _, marker := range []string{"Springfield", "Built-in Springfield playbook.", "Execute the active Springfield batch for the current project."} {
		if !strings.Contains(rendered.Content, marker) {
			t.Fatalf("expected rendered content to contain %q, got:\n%s", marker, rendered.Content)
		}
	}
	for _, unwanted := range []string{"Ralph", "Conductor"} {
		if strings.Contains(rendered.Content, unwanted) {
			t.Fatalf("expected rendered content to omit %q, got:\n%s", unwanted, rendered.Content)
		}
	}
}

func TestSkillsHaveDistinctTaskBehavior(t *testing.T) {
	t.Parallel()

	start, err := Render("start")
	if err != nil {
		t.Fatalf("render start: %v", err)
	}
	status, err := Render("status")
	if err != nil {
		t.Fatalf("render status: %v", err)
	}
	recover, err := Render("recover")
	if err != nil {
		t.Fatalf("render recover: %v", err)
	}

	if !strings.Contains(start.Content, "Execute the active Springfield batch for the current project.") {
		t.Fatalf("expected start prompt boundary to be execution-specific, got:\n%s", start.Content)
	}
	if !strings.Contains(status.Content, "Run `springfield status` to get the machine-readable view") {
		t.Fatalf("expected status prompt boundary to be status-specific, got:\n%s", status.Content)
	}
	if !strings.Contains(recover.Content, "Recover a Springfield batch that is stalled, blocked, or has a failed slice.") {
		t.Fatalf("expected recover prompt boundary to be recovery-specific, got:\n%s", recover.Content)
	}
}

func TestCanonicalCheckedInSkillsMatchRenderedContent(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, name := range []string{"plan", "start", "status", "recover"} {
		rendered, err := Render(name)
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}

		data, err := os.ReadFile(filepath.Join(root, "skills", name, "SKILL.md"))
		if err != nil {
			t.Fatalf("read checked-in skill %s: %v", name, err)
		}
		if string(data) != rendered.Content {
			t.Fatalf("checked-in skill %s did not match rendered content", name)
		}
	}
}

func TestCanonicalCheckedInCommandsMatchRenderedContent(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, name := range []string{"plan", "start", "status", "recover"} {
		rendered, err := RenderCommand(name)
		if err != nil {
			t.Fatalf("render command %s: %v", name, err)
		}

		data, err := os.ReadFile(filepath.Join(root, "commands", name+".md"))
		if err != nil {
			t.Fatalf("read checked-in command %s: %v", name, err)
		}
		if string(data) != rendered.Content {
			t.Fatalf("checked-in command %s did not match rendered content", name)
		}
	}
}

func TestRenderedSkillsIncludeFrontmatter(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"plan", "start", "status", "recover"} {
		rendered, err := Render(name)
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}

		for _, marker := range []string{
			"---\n",
			"name: " + name,
			"description:",
		} {
			if !strings.Contains(rendered.Content, marker) {
				t.Fatalf("expected rendered %s skill to contain %q, got:\n%s", name, marker, rendered.Content)
			}
		}
	}
}

func TestInstallWritesSelectedHostArtifacts(t *testing.T) {
	t.Parallel()

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
	for _, marker := range []string{"Springfield", "plan", "start", "status", "recover"} {
		if !strings.Contains(string(data), marker) {
			t.Fatalf("expected installed codex artifact to contain %q, got:\n%s", marker, string(data))
		}
	}
	// Lock plan-first ordering of the user-visible Springfield Skills bullet list.
	body := string(data)
	sectionIdx := strings.Index(body, "## Springfield Skills")
	if sectionIdx < 0 {
		t.Fatalf("installed codex helper missing '## Springfield Skills' section:\n%s", body)
	}
	section := body[sectionIdx:]
	wantOrder := []string{"- plan", "- start", "- status", "- recover"}
	last := -1
	for _, marker := range wantOrder {
		idx := strings.Index(section, marker)
		if idx < 0 {
			t.Fatalf("Springfield Skills section missing %q:\n%s", marker, section)
		}
		if idx <= last {
			t.Fatalf("Springfield Skills section out of order: %q at %d, prior marker at %d:\n%s", marker, idx, last, section)
		}
		last = idx
	}
	if _, err := os.Stat(filepath.Join(claudeDir, "springfield.md")); !os.IsNotExist(err) {
		t.Fatalf("expected codex-only install to skip claude artifact, stat err=%v", err)
	}
}

func TestInstallDefaultsCodexToAgentsSkillsDir(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	projectRoot := t.TempDir()

	oldHome := os.Getenv("HOME")
	t.Cleanup(func() {
		if oldHome == "" {
			_ = os.Unsetenv("HOME")
			return
		}
		_ = os.Setenv("HOME", oldHome)
	})
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}

	installed, err := Install(projectRoot, InstallOptions{Hosts: []string{"codex"}})
	if err != nil {
		t.Fatalf("install codex with default home: %v", err)
	}

	if len(installed) != 1 {
		t.Fatalf("installed len = %d, want 1", len(installed))
	}

	want := filepath.Join(home, ".agents", "skills", "springfield", "SKILL.md")
	if installed[0].Path != want {
		t.Fatalf("installed path = %q, want %q", installed[0].Path, want)
	}

	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read installed codex skill: %v", err)
	}
	if !strings.Contains(string(data), "name: springfield") {
		t.Fatalf("expected installed codex skill to include frontmatter, got:\n%s", string(data))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}
