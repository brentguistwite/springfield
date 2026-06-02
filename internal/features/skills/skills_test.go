package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"springfield/internal/features/playbooks"
)

func TestCatalogShapeLockedToSpringfieldSkills(t *testing.T) {
	t.Parallel()

	catalog := Catalog()
	want := []string{"plan", "jira", "status", "recover"}
	if len(catalog) != len(want) {
		t.Fatalf("catalog len = %d, want %d", len(catalog), len(want))
	}
	for i := range want {
		if string(catalog[i].Name) != want[i] {
			t.Fatalf("catalog[%d] = %q, want %q", i, catalog[i].Name, want[i])
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

func TestCatalog_IncludesJira(t *testing.T) {
	t.Parallel()

	for _, s := range Catalog() {
		if string(s.Name) == "jira" {
			if s.Purpose != playbooks.PurposePlan {
				t.Errorf("jira skill Purpose = %q, want %q", s.Purpose, playbooks.PurposePlan)
			}
			if s.RelativePath != "skills/jira/SKILL.md" {
				t.Errorf("jira skill RelativePath = %q, want skills/jira/SKILL.md", s.RelativePath)
			}
			return
		}
	}
	t.Fatalf("jira skill missing from catalog")
}

func TestLookup_Jira(t *testing.T) {
	t.Parallel()

	s, err := Lookup("jira")
	if err != nil {
		t.Fatalf("Lookup(jira): %v", err)
	}
	if string(s.Name) != "jira" {
		t.Errorf("Name = %q, want jira", s.Name)
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

// TestMinCLIVersionDoesNotExceedCurrentVersion guards the hand-maintained floor
// against accidentally drifting past the shipped CLI: a floor higher than the
// current release would tell every existing user to upgrade to something that
// does not yet exist. The floor is meant to be a *generous* minimum, bumped
// only when a skill starts needing a new capability — never preemptively.
func TestMinCLIVersionDoesNotExceedCurrentVersion(t *testing.T) {
	t.Parallel()

	// Walk up to repo root so we can read version.txt independent of cwd.
	_, file, _, _ := runtime.Caller(0)
	root := file
	for {
		root = filepath.Dir(root)
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		if filepath.Dir(root) == root {
			t.Fatal("could not locate go.mod from skills_test.go")
		}
	}

	data, err := os.ReadFile(filepath.Join(root, "version.txt"))
	if err != nil {
		t.Fatalf("read version.txt: %v", err)
	}
	current := strings.TrimSpace(string(data))

	if cmp := compareSemver(t, MinCLIVersion, current); cmp > 0 {
		t.Fatalf("MinCLIVersion (%s) must not exceed current version.txt (%s) — a floor above the shipped CLI tells every user to upgrade to a release that does not exist", MinCLIVersion, current)
	}
}

// compareSemver returns -1, 0, or 1 for MAJOR.MINOR.PATCH triples.
func compareSemver(t *testing.T, a, b string) int {
	t.Helper()
	parse := func(s string) [3]int {
		s = strings.TrimPrefix(s, "v")
		parts := strings.Split(s, ".")
		if len(parts) != 3 {
			t.Fatalf("not strict MAJOR.MINOR.PATCH: %q", s)
		}
		var out [3]int
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				t.Fatalf("non-integer semver component in %q: %v", s, err)
			}
			out[i] = n
		}
		return out
	}
	ap, bp := parse(a), parse(b)
	for i := range ap {
		switch {
		case ap[i] < bp[i]:
			return -1
		case ap[i] > bp[i]:
			return 1
		}
	}
	return 0
}

// TestRenderedSkillsCarryVersionCheckPreamble pins that every skill (and its
// command form) opens with the actionable CLI floor check: run the version
// command, and when the CLI is missing or older than MinCLIVersion, surface the
// exact brew install/upgrade command rather than failing cryptically.
func TestRenderedSkillsCarryVersionCheckPreamble(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"plan", "jira", "status", "recover"} {
		skill, err := Render(name)
		if err != nil {
			t.Fatalf("Render(%s): %v", name, err)
		}
		command, err := RenderCommand(name)
		if err != nil {
			t.Fatalf("RenderCommand(%s): %v", name, err)
		}

		for label, content := range map[string]string{"skill": skill.Content, "command": command.Content} {
			for _, want := range []string{
				"springfield version",
				MinCLIVersion,
				"brew install brentguistwite/tap/springfield",
				"brew upgrade springfield",
			} {
				if !strings.Contains(content, want) {
					t.Errorf("%s %s missing version-check token %q", name, label, want)
				}
			}
		}
	}
}

// TestPlanSkillRequiresExplicitDocTargets pins the C2 constraint: any
// acceptance criterion that prescribes documentation must name an explicit
// target file path. Vague targets ("in the review docs") triggered ~75 turns
// of an agent hunting for a doc file that never existed in a dogfood batch.
// The constraint must carry that rationale plus an allowed/forbidden example,
// and it must reach both the skill and command renders.
func TestPlanSkillRequiresExplicitDocTargets(t *testing.T) {
	t.Parallel()

	skill, err := Render("plan")
	if err != nil {
		t.Fatalf("Render(plan): %v", err)
	}
	command, err := RenderCommand("plan")
	if err != nil {
		t.Fatalf("RenderCommand(plan): %v", err)
	}

	for label, content := range map[string]string{"skill": skill.Content, "command": command.Content} {
		// Clearly-flagged constraint requiring an explicit target file path.
		if !strings.Contains(content, "must name an explicit target file") {
			t.Errorf("plan %s missing docs-target constraint", label)
		}
		// Rationale grounded in dogfood evidence of thrash on vague targets.
		if !strings.Contains(content, "dogfood") {
			t.Errorf("plan %s docs-target constraint missing dogfood rationale", label)
		}
		// Allowed vs forbidden example wording.
		if !strings.Contains(content, "Allowed:") || !strings.Contains(content, "Forbidden:") {
			t.Errorf("plan %s docs-target constraint missing allowed/forbidden example", label)
		}
		// Forbidden vague phrasings are named explicitly so the agent can pattern-match.
		if !strings.Contains(content, "in the review docs") {
			t.Errorf("plan %s docs-target constraint missing forbidden 'in the review docs' phrasing", label)
		}
	}
}

// TestPlanSkillHandHoldsDefinitionOfDone pins the Definition-of-Done step and
// the review offer into the generated plan skill + command. The prose lives in
// the TaskBody Go literal (internal/features/skills/types.go) and is regenerated
// onto disk by cmd/regen; this guard fails if a future regen silently drops it,
// or before the literal carries it (its red-first role during implementation).
func TestPlanSkillHandHoldsDefinitionOfDone(t *testing.T) {
	t.Parallel()

	skill, err := Render("plan")
	if err != nil {
		t.Fatalf("Render(plan): %v", err)
	}
	command, err := RenderCommand("plan")
	if err != nil {
		t.Fatalf("RenderCommand(plan): %v", err)
	}

	for label, content := range map[string]string{"skill": skill.Content, "command": command.Content} {
		// The DoD step exists and is named.
		if !strings.Contains(content, "Definition of Done") {
			t.Errorf("plan %s missing Definition of Done step", label)
		}
		// It asks the user how a slice is verified.
		if !strings.Contains(content, "how do we know this is done") {
			t.Errorf("plan %s missing the 'how do we know this is done' prompt", label)
		}
		// Honest framing: criteria are prompt input, not a deterministic gate.
		if !strings.Contains(content, "sharpen the agent and reviewer prompts") {
			t.Errorf("plan %s missing criteria-as-prompt-input framing", label)
		}
		// The marker — not the criteria — is the completion gate.
		if !strings.Contains(content, "<story-pass>") {
			t.Errorf("plan %s missing <story-pass> marker explanation", label)
		}
		// Review gate is the only independent check.
		if !strings.Contains(content, "the only independent check") {
			t.Errorf("plan %s missing review-gate honesty line", label)
		}
		// Separate review-offer prompt, leading with the per-plan toggle.
		if !strings.Contains(content, "Enable independent pre-merge review") {
			t.Errorf("plan %s missing review offer", label)
		}
		if !strings.Contains(content, "plans[].review") {
			t.Errorf("plan %s review offer missing per-plan toggle", label)
		}
		// Global operator-wide lever named as secondary.
		if !strings.Contains(content, "springfield.local.toml") {
			t.Errorf("plan %s review offer missing global config reference", label)
		}
		// Load-bearing imperative: the answer must be serialized into the
		// envelope, not just asked about (else review silently no-ops).
		if !strings.Contains(content, "Serialize the answer") {
			t.Errorf("plan %s missing the serialize-the-answer instruction", label)
		}
		// The example scaffold the model copies must show review explicitly,
		// not omit it (an omitted field reads as "inherit default" = off).
		if !strings.Contains(content, "\"review\": true") {
			t.Errorf("plan %s example envelope must set review explicitly", label)
		}
		// The example must be homogeneous: a mixed true/false scaffold teaches
		// the model to split review across plans, contradicting "same value on
		// every plan" and silently leaving some plans unreviewed.
		if strings.Contains(content, "\"review\": false") {
			t.Errorf("plan %s example must not mix review values (found \"review\": false)", label)
		}
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

	def, err := Lookup("plan")
	if err != nil {
		t.Fatalf("lookup plan: %v", err)
	}

	rendered, err := Render(string(def.Name))
	if err != nil {
		t.Fatalf("render plan: %v", err)
	}

	out, err := playbooks.Build(playbooks.Input{
		Purpose:               playbooks.PurposePlan,
		IncludeProjectContext: false,
		TaskBody:              def.TaskBody,
	})
	if err != nil {
		t.Fatalf("build plan playbook: %v", err)
	}

	if rendered.Prompt != out.Prompt {
		t.Fatalf("expected prompt to come from shared playbook builder")
	}
	for _, marker := range []string{"Springfield", "Built-in Springfield playbook.", "Compile a Springfield batch from the user's work request."} {
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

	plan, err := Render("plan")
	if err != nil {
		t.Fatalf("render plan: %v", err)
	}
	status, err := Render("status")
	if err != nil {
		t.Fatalf("render status: %v", err)
	}
	recover, err := Render("recover")
	if err != nil {
		t.Fatalf("render recover: %v", err)
	}

	if !strings.Contains(plan.Content, "Compile a Springfield batch from the user's work request.") {
		t.Fatalf("expected plan prompt boundary to be planning-specific, got:\n%s", plan.Content)
	}
	if !strings.Contains(plan.Content, "slice boundaries") {
		t.Fatalf("expected plan prompt to describe slice boundaries, got:\n%s", plan.Content)
	}
	if !strings.Contains(status.Content, "Run `springfield status` to get the current Springfield batch state") {
		t.Fatalf("expected status prompt boundary to be status-specific, got:\n%s", status.Content)
	}
	if !strings.Contains(status.Content, "Which slices are done, running, blocked, or queued") {
		t.Fatalf("expected status prompt to describe slice progress, got:\n%s", status.Content)
	}
	if !strings.Contains(recover.Content, "Recover a Springfield batch that is stalled, blocked, or has a failed slice.") {
		t.Fatalf("expected recover prompt boundary to be recovery-specific, got:\n%s", recover.Content)
	}
}

func TestCanonicalCheckedInSkillsMatchRenderedContent(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, name := range []string{"plan", "jira", "status", "recover"} {
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
	for _, name := range []string{"plan", "jira", "status", "recover"} {
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

	for _, name := range []string{"plan", "jira", "status", "recover"} {
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
	for _, marker := range []string{"Springfield", "plan", "jira", "status", "recover"} {
		if !strings.Contains(string(data), marker) {
			t.Fatalf("expected installed codex artifact to contain %q, got:\n%s", marker, string(data))
		}
	}
	if strings.Contains(string(data), "start") {
		t.Fatalf("installed codex artifact must not reference start skill, got:\n%s", string(data))
	}
	// Lock plan-first ordering of the user-visible Springfield Skills bullet list.
	body := string(data)
	sectionIdx := strings.Index(body, "## Springfield Skills")
	if sectionIdx < 0 {
		t.Fatalf("installed codex helper missing '## Springfield Skills' section:\n%s", body)
	}
	section := body[sectionIdx:]
	wantOrder := []string{"- plan", "- jira", "- status", "- recover"}
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

// TestInstallDoesNotMutateGeminiSettings locks the invariant that the
// skills installer never touches ~/.gemini/settings.json. Gemini's
// control-plane hook is injected per-invocation via
// GEMINI_CLI_SYSTEM_SETTINGS_PATH — the installer must stay out of the
// user's global Gemini config.
func TestInstallDoesNotMutateGeminiSettings(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)

	geminiDir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatalf("mkdir gemini: %v", err)
	}
	settingsPath := filepath.Join(geminiDir, "settings.json")
	original := `{"some":"user","config":true}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	origStat, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if _, err := Install(projectRoot, InstallOptions{}); err != nil {
		t.Fatalf("install: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read after install: %v", err)
	}
	if string(data) != original {
		t.Fatalf("gemini settings.json was mutated by Install; want %q, got %q", original, string(data))
	}
	newStat, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !newStat.ModTime().Equal(origStat.ModTime()) {
		t.Fatalf("gemini settings.json mtime changed by Install: %v -> %v", origStat.ModTime(), newStat.ModTime())
	}
}

func TestInstallDefaultsCodexToAgentsSkillsDir(t *testing.T) {
	// HOME is process-global; t.Setenv serializes restore and forbids parallel
	// ancestors, which previously raced TestInstallDoesNotMutateUserSettings.

	home := t.TempDir()
	projectRoot := t.TempDir()

	t.Setenv("HOME", home)

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

// TestInstallDoesNotMutateUserSettings verifies that Install never touches
// $HOME/.claude/settings.json. The hook-guard is wired per-subagent via the
// spawned agent's --settings flag; it must never pollute the user's global
// Claude settings.
func TestInstallDoesNotMutateUserSettings(t *testing.T) {
	// HOME is process-global; t.Setenv serializes restore and forbids parallel
	// ancestors, which previously raced TestInstallDefaultsCodexToAgentsSkillsDir.

	home := t.TempDir()
	projectRoot := t.TempDir()

	claudeHome := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeHome, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	settingsPath := filepath.Join(claudeHome, "settings.json")
	original := []byte(`{"some":"user","setting":42}`)
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatalf("write stub settings.json: %v", err)
	}

	t.Setenv("HOME", home)

	claudeDir := filepath.Join(home, ".claude", "commands")
	codexDir := filepath.Join(home, ".agents", "skills")
	if _, err := Install(projectRoot, InstallOptions{
		ClaudeDir: claudeDir,
		CodexDir:  codexDir,
	}); err != nil {
		t.Fatalf("install: %v", err)
	}

	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings.json after install: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("install mutated $HOME/.claude/settings.json\nbefore: %s\nafter:  %s", original, after)
	}
}

func TestLookupRejectsRemovedStartSkill(t *testing.T) {
	t.Parallel()

	_, err := Lookup("start")
	if err == nil {
		t.Fatal("Lookup(start) should return error, got nil")
	}
	if !strings.Contains(err.Error(), `unknown Springfield skill "start"`) {
		t.Fatalf("Lookup(start) error = %q, want to contain %q", err.Error(), `unknown Springfield skill "start"`)
	}
}

func TestSkillsCatalogOmitsStart(t *testing.T) {
	t.Parallel()

	for _, s := range Catalog() {
		if string(s.Name) == "start" {
			t.Fatalf("catalog must not contain start skill, but found entry: %+v", s)
		}
	}
}

func TestInstalledPluginCatalogOmitsStart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude", "commands")
	codexDir := filepath.Join(root, ".codex", "skills")

	_, err := Install(root, InstallOptions{
		Hosts:     []string{"claude-code"},
		ClaudeDir: claudeDir,
		CodexDir:  codexDir,
	})
	if err != nil {
		t.Fatalf("install claude-code: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(claudeDir, "springfield.md"))
	if err != nil {
		t.Fatalf("read installed claude-code artifact: %v", err)
	}
	body := string(data)
	if strings.Contains(body, "Springfield Start") {
		t.Fatalf("installed claude-code artifact must not contain 'Springfield Start', got:\n%s", body)
	}
	if strings.Contains(body, "springfield:start") {
		t.Fatalf("installed claude-code artifact must not contain 'springfield:start', got:\n%s", body)
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

// TestJiraSkillIngestsTickets pins the jira-ingest contract into the generated
// skill + command. The prose lives in the TaskBody literal (types.go) and is
// regenerated onto disk by cmd/regen; this guard fails if a future regen drops
// it, or (its red-first role) before the literal carries it.
func TestJiraSkillIngestsTickets(t *testing.T) {
	t.Parallel()

	skill, err := Render("jira")
	if err != nil {
		t.Fatalf("Render(jira): %v", err)
	}
	command, err := RenderCommand("jira")
	if err != nil {
		t.Fatalf("RenderCommand(jira): %v", err)
	}

	for label, content := range map[string]string{"skill": skill.Content, "command": command.Content} {
		for _, want := range []string{
			// BYO-Jira precondition, not setup.
			"Springfield does NOT manage Jira access",
			"No Jira tool detected",
			// Input + epic expansion guardrail.
			"give me an epic key",
			"These N tickets",
			// Mapping grain: ticket->plan (id from key), subtask->story.
			"Slug the plan",
			"subtasks",
			"user stories",
			// Ordering from Jira links.
			"topologically sort",
			// DoD reuse + bulk escape hatch.
			"how do we know this story is done",
			"don't ask me about criteria",
			// Honest criteria framing (lifted from plan skill).
			"<story-pass>",
			"the only independent check",
			// Review offer, serialized.
			"Enable independent pre-merge review",
			"Serialize the answer",
			"plans[].review",
			// Read-only boundary.
			"does NOT write back to Jira",
			// Contract clauses hardened during adversarial review — load-bearing,
			// pinned so a future prose rewrite can't silently drop them.
			"parent-acceptance",                                 // parent ticket's DoD survives subtask expansion
			"different parent ticket",                           // cross-ticket subtask blocks links are skipped
			"story dependency graph blocked: no eligible story", // story-dep cycle degrades, not hard-fails
			"never started",                                     // status can't prove pristineness — "nothing running" != "never started"
			"Has this batch ever been started",                  // the human is the gate: status output is not trustworthy for this
			"try to reverse the queued plan ids",                // don't reconstruct Jira keys from lossy slugs — user supplies keys
			"--replace --prd -",                                 // active-batch persist uses --replace (plain --prd - hard-errors)
		} {
			if !strings.Contains(content, want) {
				t.Errorf("jira %s missing token %q", label, want)
			}
		}
	}
}
