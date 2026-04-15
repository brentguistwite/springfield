package playbooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPurposesLoadSharedBuiltin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		purpose Purpose
		task    string
	}{
		{name: "plan", purpose: PurposePlan, task: "Ship the change."},
		{name: "start", purpose: PurposeStart, task: "Start Springfield work."},
		{name: "status", purpose: PurposeStatus, task: "Inspect Springfield work."},
		{name: "recover", purpose: PurposeRecover, task: "Recover Springfield work."},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := Build(Input{
				Purpose:               tc.purpose,
				ProjectRoot:           t.TempDir(),
				IncludeProjectContext: false,
				TaskBody:              tc.task,
			})
			if err != nil {
				t.Fatalf("build %s playbook: %v", tc.name, err)
			}

			if out.BuiltinSource != "builtin/springfield.md" {
				t.Fatalf("builtin source = %q", out.BuiltinSource)
			}
			if !strings.Contains(out.Prompt, "Built-in Springfield playbook.") {
				t.Fatalf("expected shared builtin content, got:\n%s", out.Prompt)
			}
			assertNoLegacyEngineNames(t, out.Prompt)
		})
	}
}

func TestProjectContextPrefersAgents(t *testing.T) {
	root := t.TempDir()
	writeContextFile(t, root, "AGENTS.md", "agents context")
	writeContextFile(t, root, "CLAUDE.md", "claude context")

	out, err := Build(Input{
		Purpose:               PurposePlan,
		ProjectRoot:           root,
		IncludeProjectContext: true,
		TaskBody:              "Ship the change.",
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
		Purpose:               PurposePlan,
		ProjectRoot:           root,
		IncludeProjectContext: true,
		TaskBody:              "Ship the change.",
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
		Purpose:               PurposeRecover,
		ProjectRoot:           root,
		IncludeProjectContext: true,
		TaskBody:              "task body",
	})
	if err != nil {
		t.Fatalf("build playbook: %v", err)
	}

	builtinIndex := strings.Index(out.Prompt, "Built-in Springfield playbook.")
	projectIndex := strings.Index(out.Prompt, "project guidance")
	taskIndex := strings.Index(out.Prompt, "task body")
	if builtinIndex == -1 || projectIndex == -1 || taskIndex == -1 {
		t.Fatalf("expected builtin, project, and task sections in prompt, got:\n%s", out.Prompt)
	}
	if !(builtinIndex < projectIndex && projectIndex < taskIndex) {
		t.Fatalf("expected stable builtin -> project -> task order, got:\n%s", out.Prompt)
	}
	assertNoLegacyEngineNames(t, out.Prompt)
}

func TestOmitProjectContextLeavesProjectSectionOut(t *testing.T) {
	root := t.TempDir()
	writeContextFile(t, root, "AGENTS.md", "project guidance")

	out, err := Build(Input{
		Purpose:               PurposeStatus,
		ProjectRoot:           root,
		IncludeProjectContext: false,
		TaskBody:              "task body",
	})
	if err != nil {
		t.Fatalf("build playbook: %v", err)
	}

	if out.ProjectSource != "" {
		t.Fatalf("project source = %q, want empty", out.ProjectSource)
	}
	if strings.Contains(out.Prompt, "project guidance") {
		t.Fatalf("expected prompt to omit project context, got:\n%s", out.Prompt)
	}
	if !strings.Contains(out.Prompt, "task body") {
		t.Fatalf("expected task body in prompt, got:\n%s", out.Prompt)
	}
}

func writeContextFile(t *testing.T, root, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func assertNoLegacyEngineNames(t *testing.T, body string) {
	t.Helper()

	for _, legacy := range []string{"Ralph", "Conductor"} {
		if strings.Contains(body, legacy) {
			t.Fatalf("expected Springfield-owned prompt surface to omit %q, got:\n%s", legacy, body)
		}
	}
}
