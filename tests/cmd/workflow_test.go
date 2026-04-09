package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/planner"
	"springfield/internal/features/workflow"
)

func TestSpringfieldStatusReadsApprovedWorkState(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")
	writeApprovedWorkflowDraft(t, dir, planner.SplitSingle)

	output, err := runBinaryIn(t, bin, dir, "status", "--work", "wave-c2")
	if err != nil {
		t.Fatalf("springfield status failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Work: wave-c2",
		"Title: Unified execution surface",
		"Split: single",
		"Status: ready",
		"01  ready  Execution adapter",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected status output to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldResumeRunsApprovedWorkByWorkID(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")
	writeApprovedWorkflowDraft(t, dir, planner.SplitSingle)

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err := runBinaryInWithEnv(
		t,
		bin,
		dir,
		[]string{"PATH=" + fakeBinDir},
		"resume", "--work", "wave-c2",
	)
	if err != nil {
		t.Fatalf("springfield resume failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Work wave-c2: completed") {
		t.Fatalf("expected completed resume output, got:\n%s", output)
	}

	statusOutput, statusErr := runBinaryIn(t, bin, dir, "status", "--work", "wave-c2")
	if statusErr != nil {
		t.Fatalf("springfield status after resume failed: %v\n%s", statusErr, statusOutput)
	}
	if !strings.Contains(statusOutput, "Status: completed") {
		t.Fatalf("expected completed workflow status, got:\n%s", statusOutput)
	}

	args := readRecordedArgs(t, argvPath)
	for _, want := range []string{"-p", "--output-format", "stream-json", "--verbose"} {
		if !containsArg(args, want) {
			t.Fatalf("expected recorded args to contain %q, got %v", want, args)
		}
	}
}

func TestSpringfieldDiagnoseRendersFailureGuidance(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")
	writeApprovedWorkflowDraft(t, dir, planner.SplitSingle)
	writeWorkflowRunState(t, dir, map[string]any{
		"status":    "failed",
		"approved":  true,
		"split":     "single",
		"error":     "agent failed",
		"workstreams": []string{"01"},
		"workstream_states": []map[string]string{
			{
				"name":          "01",
				"status":        "failed",
				"error":         "agent failed",
				"evidence_path": ".springfield/work/wave-c2/logs/01.log",
			},
		},
	})

	output, err := runBinaryIn(t, bin, dir, "diagnose", "--work", "wave-c2")
	if err != nil {
		t.Fatalf("springfield diagnose failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Work: wave-c2",
		"Status: failed",
		"01  Execution adapter",
		"Evidence: .springfield/work/wave-c2/logs/01.log",
		"resume",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected diagnose output to contain %q, got:\n%s", marker, output)
		}
	}
}

func writeApprovedWorkflowDraft(t *testing.T, dir string, split planner.Split) {
	t.Helper()

	workstreams := []planner.Workstream{{Name: "01", Title: "Execution adapter", Summary: "Route one workstream through the unified runner."}}
	if split == planner.SplitMulti {
		workstreams = []planner.Workstream{
			{Name: "01", Title: "CLI surface"},
			{Name: "02", Title: "TUI surface"},
		}
	}

	if err := workflow.WriteDraft(dir, workflow.Draft{
		RequestBody: "Implement Wave C2.",
		Response: planner.Response{
			Mode:        planner.ModeDraft,
			WorkID:      "wave-c2",
			Title:       "Unified execution surface",
			Summary:     "Route approved Springfield work through one execution runner.",
			Split:       split,
			Workstreams: workstreams,
		},
	}); err != nil {
		t.Fatalf("WriteDraft: %v", err)
	}
}

func writeWorkflowRunState(t *testing.T, dir string, value any) {
	t.Helper()

	path := filepath.Join(dir, ".springfield", "work", "wave-c2", "run-state.json")
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal run-state: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write run-state: %v", err)
	}
}
