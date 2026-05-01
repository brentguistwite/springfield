package runtime_test

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/config"
	"springfield/internal/features/execution"
)

func TestFailedSlicePopulatesEvidencePath(t *testing.T) {
	root := t.TempDir()
	if _, err := config.Init(root, []string{"claude"}, config.InitOptions{}); err != nil {
		t.Fatalf("config.Init: %v", err)
	}

	installRuntimeAgentScript(t, root, `#!/bin/sh
echo 'agent stderr line' 1>&2
exit 1
`)
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))

	runner, err := execution.NewRuntimeRunner(root, osexec.LookPath, nil)
	if err != nil {
		t.Fatalf("NewRuntimeRunner: %v", err)
	}

	report, runErr := runner.Run(root, execution.Work{
		ID:          "b-2026-05-02-1234-slice-001",
		Title:       "Evidence capture",
		RequestBody: "Implement evidence capture.",
		Split:       "single",
		Workstreams: []execution.Workstream{{Name: "slice-001", Title: "Slice 001", Summary: "fail once"}},
	})
	if runErr == nil {
		t.Fatal("expected runtime failure")
	}
	if report.Status != "failed" {
		t.Fatalf("report status = %q, want failed", report.Status)
	}
	if len(report.Workstreams) != 1 {
		t.Fatalf("workstreams = %d, want 1", len(report.Workstreams))
	}

	evidenceDir := filepath.Join(root, ".springfield", "plans", "b-2026-05-02-1234", "evidence", "slice-001")
	if got := report.Workstreams[0].EvidencePath; got != evidenceDir {
		t.Fatalf("evidence path = %q, want %q", got, evidenceDir)
	}
	assertEvidenceFilesExist(t, evidenceDir)
}

func TestSuccessfulSlicePopulatesEvidencePath(t *testing.T) {
	root := t.TempDir()
	if _, err := config.Init(root, []string{"claude"}, config.InitOptions{}); err != nil {
		t.Fatalf("config.Init: %v", err)
	}

	installRuntimeAgentScript(t, root, `#!/bin/sh
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_fake"},{"type":"tool_result","tool_use_id":"toolu_fake","is_error":false}]}}'
echo 'assistant-output'
exit 0
`)
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))

	runner, err := execution.NewRuntimeRunner(root, osexec.LookPath, nil)
	if err != nil {
		t.Fatalf("NewRuntimeRunner: %v", err)
	}

	report, runErr := runner.Run(root, execution.Work{
		ID:          "b-2026-05-02-5678-slice-002",
		Title:       "Evidence capture",
		RequestBody: "Implement evidence capture.",
		Split:       "single",
		Workstreams: []execution.Workstream{{Name: "slice-002", Title: "Slice 002", Summary: "succeed once"}},
	})
	if runErr != nil {
		t.Fatalf("unexpected runtime failure: %v", runErr)
	}
	if report.Status != "completed" {
		t.Fatalf("report status = %q, want completed", report.Status)
	}
	if len(report.Workstreams) != 1 {
		t.Fatalf("workstreams = %d, want 1", len(report.Workstreams))
	}

	evidenceDir := filepath.Join(root, ".springfield", "plans", "b-2026-05-02-5678", "evidence", "slice-002")
	if got := report.Workstreams[0].EvidencePath; got != evidenceDir {
		t.Fatalf("evidence path = %q, want %q", got, evidenceDir)
	}
	assertEvidenceFilesExist(t, evidenceDir)

	assistantText, err := os.ReadFile(filepath.Join(evidenceDir, "assistant_text.txt"))
	if err != nil {
		t.Fatalf("ReadFile(assistant_text.txt): %v", err)
	}
	if !strings.Contains(string(assistantText), "assistant-output") {
		t.Fatalf("assistant_text.txt missing assistant output: %q", string(assistantText))
	}
}

func installRuntimeAgentScript(t *testing.T, root, script string) string {
	t.Helper()

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bin): %v", err)
	}
	path := filepath.Join(binDir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent: %v", err)
	}
	return path
}

func assertEvidenceFilesExist(t *testing.T, evidenceDir string) {
	t.Helper()

	for _, name := range []string{"meta.json", "events.jsonl", "assistant_text.txt", "prompt.txt"} {
		if _, err := os.Stat(filepath.Join(evidenceDir, name)); err != nil {
			t.Fatalf("Stat(%s): %v", name, err)
		}
	}
}
