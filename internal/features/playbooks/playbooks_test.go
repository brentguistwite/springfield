package playbooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanPurposeLoadsSharedBuiltin(t *testing.T) {
	out, err := Build(Input{
		Purpose:     PurposePlan,
		ProjectRoot: t.TempDir(),
		TaskBody:    "Ship the change.",
	})
	if err != nil {
		t.Fatalf("build plan playbook: %v", err)
	}

	if out.BuiltinSource != "builtin/conductor.md" {
		t.Fatalf("builtin source = %q", out.BuiltinSource)
	}
	if !strings.Contains(out.Prompt, "Built-in Conductor playbook.") {
		t.Fatalf("expected shared builtin content, got:\n%s", out.Prompt)
	}
}

func TestExplainPurposeLoadsSharedBuiltin(t *testing.T) {
	out, err := Build(Input{
		Purpose:     PurposeExplain,
		ProjectRoot: t.TempDir(),
		TaskBody:    "Run the workstreams.",
	})
	if err != nil {
		t.Fatalf("build explain playbook: %v", err)
	}

	if out.BuiltinSource != "builtin/conductor.md" {
		t.Fatalf("builtin source = %q", out.BuiltinSource)
	}
	if !strings.Contains(out.Prompt, "Built-in Conductor playbook.") {
		t.Fatalf("expected shared builtin content, got:\n%s", out.Prompt)
	}
}

func TestProjectContextPrefersAgents(t *testing.T) {
	root := t.TempDir()
	writeContextFile(t, root, "AGENTS.md", "agents context")
	writeContextFile(t, root, "CLAUDE.md", "claude context")

	out, err := Build(Input{
		Purpose:     PurposePlan,
		ProjectRoot: root,
		TaskBody:    "Ship the change.",
	})
	if err != nil {
		t.Fatalf("build playbook: %v", err)
	}

	if out.ProjectSource != filepath.Join(root, "AGENTS.md") {
		t.Fatalf("project source = %q", out.ProjectSource)
	}
	if !strings.Contains(out.Prompt, "agents context") {
		t.Fatalf("expected AGENTS context, got:\n%s", out.Prompt)
	}
	if strings.Contains(out.Prompt, "claude context") {
		t.Fatalf("expected CLAUDE context to be ignored when AGENTS exists, got:\n%s", out.Prompt)
	}
}

func TestProjectContextFallsBackToClaude(t *testing.T) {
	root := t.TempDir()
	writeContextFile(t, root, "CLAUDE.md", "claude context")

	out, err := Build(Input{
		Purpose:     PurposePlan,
		ProjectRoot: root,
		TaskBody:    "Ship the change.",
	})
	if err != nil {
		t.Fatalf("build playbook: %v", err)
	}

	if out.ProjectSource != filepath.Join(root, "CLAUDE.md") {
		t.Fatalf("project source = %q", out.ProjectSource)
	}
	if !strings.Contains(out.Prompt, "claude context") {
		t.Fatalf("expected CLAUDE context, got:\n%s", out.Prompt)
	}
}

func TestRenderIncludesSectionsInOrder(t *testing.T) {
	root := t.TempDir()
	writeContextFile(t, root, "AGENTS.md", "project guidance")

	out, err := Build(Input{
		Purpose:     PurposeExplain,
		ProjectRoot: root,
		TaskBody:    "task body",
	})
	if err != nil {
		t.Fatalf("build playbook: %v", err)
	}

	builtinIndex := strings.Index(out.Prompt, "Built-in Conductor playbook.")
	projectIndex := strings.Index(out.Prompt, "project guidance")
	taskIndex := strings.Index(out.Prompt, "task body")
	if builtinIndex == -1 || projectIndex == -1 || taskIndex == -1 {
		t.Fatalf("expected builtin, project, and task sections in prompt, got:\n%s", out.Prompt)
	}
	if !(builtinIndex < projectIndex && projectIndex < taskIndex) {
		t.Fatalf("expected stable builtin -> project -> task order, got:\n%s", out.Prompt)
	}
}

func writeContextFile(t *testing.T, root, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
