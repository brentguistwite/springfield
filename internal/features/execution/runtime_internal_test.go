package execution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
)

func fakeRuntimeSuccess(_ context.Context, cmd exec.Command, handler exec.EventHandler) exec.Result {
	// Positive-signal contract: emit a tool_use/tool_result success pair
	// (collapsed into one assistant message so the existing single-event
	// expectations stay valid) so ValidateResult sees a real completion.
	events := []exec.Event{
		{
			Type: exec.EventStdout,
			Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"},{"type":"tool_result","tool_use_id":"toolu_01","is_error":false}]}}`,
			Time: time.Now(),
		},
	}
	if handler != nil {
		handler(events[0])
	}
	return exec.Result{ExitCode: 0, Events: events}
}

func fakeRuntimeFailure(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
	return exec.Result{ExitCode: 1, Err: nil}
}

func testRuntimeRegistry() agents.Registry {
	return agents.NewRegistry(
		claude.New(fakeRuntimeLookPath),
		codex.New(fakeRuntimeLookPath),
	)
}

func fakeRuntimeLookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

func TestRuntimeSingleExecutorRunPassesWorkstreamThroughSharedRuntime(t *testing.T) {
	registry := testRuntimeRegistry()
	var calls []exec.Command
	fakeRun := func(_ context.Context, cmd exec.Command, handler exec.EventHandler) exec.Result {
		calls = append(calls, cmd)
		return fakeRuntimeSuccess(context.Background(), cmd, handler)
	}
	runner := coreruntime.NewTestRunner(registry, fakeRun, time.Now)
	executor := runtimeSingleExecutor{
		runner:   runner,
		agents:   []agents.ID{agents.AgentClaude},
		workDir:  t.TempDir(),
		settings: agents.ExecutionSettings{},
	}

	report, err := executor.Run("", Work{
		ID:          "wave-a1",
		Title:       "Execution seam",
		RequestBody: "Implement the runtime seam.",
		Split:       "single",
		Workstreams: []Workstream{
			{Name: "01", Title: "Adapter", Summary: "Route one workstream."},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.Status != statusCompleted {
		t.Fatalf("status = %q, want %q", report.Status, statusCompleted)
	}
	if len(report.Workstreams) != 1 {
		t.Fatalf("workstreams = %d, want 1", len(report.Workstreams))
	}
	if report.Workstreams[0].Status != statusCompleted {
		t.Fatalf("workstream status = %q, want %q", report.Workstreams[0].Status, statusCompleted)
	}
	if len(calls) != 1 {
		t.Fatalf("runtime calls = %d, want 1", len(calls))
	}

	for _, want := range []string{"Implement the runtime seam.", "Adapter", "Route one workstream."} {
		if !strings.Contains(calls[0].Stdin, want) {
			t.Fatalf("expected stdin to contain %q, got %s", want, calls[0].Stdin)
		}
	}
}

func TestRuntimeSingleExecutorRunReturnsFailedReportOnRuntimeFailure(t *testing.T) {
	registry := testRuntimeRegistry()
	// NewTestRunner marks non-zero exits as StatusFailed, so ExitCode=1 is enough here.
	runner := coreruntime.NewTestRunner(registry, fakeRuntimeFailure, time.Now)
	executor := runtimeSingleExecutor{
		runner:   runner,
		agents:   []agents.ID{agents.AgentClaude},
		workDir:  t.TempDir(),
		settings: agents.ExecutionSettings{},
	}

	report, err := executor.Run("", Work{
		ID:    "wave-a1",
		Title: "Execution seam",
		Split: "single",
		Workstreams: []Workstream{
			{Name: "01", Title: "Adapter"},
		},
	})
	if err == nil {
		t.Fatal("expected Run to fail")
	}
	if report.Status != statusFailed {
		t.Fatalf("status = %q, want %q", report.Status, statusFailed)
	}
	if report.Error == "" {
		t.Fatal("expected failed report to include error")
	}
	if len(report.Workstreams) != 1 || report.Workstreams[0].Status != statusFailed {
		t.Fatalf("workstream report = %#v, want failed status", report.Workstreams)
	}
}

func TestRuntimeSingleExecutorRejectsMultipleWorkstreams(t *testing.T) {
	executor := runtimeSingleExecutor{}

	_, err := executor.Run("", Work{
		ID:    "wave-a1",
		Title: "Execution seam",
		Split: "single",
		Workstreams: []Workstream{
			{Name: "01", Title: "Adapter"},
			{Name: "02", Title: "Status"},
		},
	})
	if err == nil {
		t.Fatal("expected Run to reject multiple workstreams")
	}
	want := `work "wave-a1" split "single" requires exactly one workstream, got 2`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestExecutionPromptIncludesSliceBody(t *testing.T) {
	work := Work{
		ID:    "batch-01",
		Title: "Batch title",
		Workstreams: []Workstream{
			{Name: "01", Title: "Slice title", Summary: "implement X"},
		},
	}
	prompt, err := executionPrompt("", work, work.Workstreams[0])
	if err != nil {
		t.Fatalf("executionPrompt: %v", err)
	}
	if !strings.Contains(prompt, "implement X") {
		t.Fatalf("expected prompt to contain slice summary %q, got:\n%s", "implement X", prompt)
	}
}

func TestExecutionPromptIncludesAgentsMdWhenPresent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("foo bar"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	work := Work{
		ID:    "batch-01",
		Title: "Batch title",
		Workstreams: []Workstream{
			{Name: "01", Title: "Slice title", Summary: "do it"},
		},
	}
	prompt, err := executionPrompt(root, work, work.Workstreams[0])
	if err != nil {
		t.Fatalf("executionPrompt: %v", err)
	}
	if !strings.Contains(prompt, "foo bar") {
		t.Fatalf("expected prompt to contain AGENTS.md content, got:\n%s", prompt)
	}
}

func TestExecutionPromptConcatenatesAgentsClaudeGemini(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"AGENTS.md": "agents-content",
		"CLAUDE.md": "claude-content",
		"GEMINI.md": "gemini-content",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	work := Work{
		ID:    "batch-01",
		Title: "Batch title",
		Workstreams: []Workstream{
			{Name: "01", Title: "Slice title", Summary: "do it"},
		},
	}
	prompt, err := executionPrompt(root, work, work.Workstreams[0])
	if err != nil {
		t.Fatalf("executionPrompt: %v", err)
	}
	for _, want := range []string{"agents-content", "claude-content", "gemini-content"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got:\n%s", want, prompt)
		}
	}
	// Check section headers appear
	for _, header := range []string{"## AGENTS.md", "## CLAUDE.md", "## GEMINI.md"} {
		if !strings.Contains(prompt, header) {
			t.Fatalf("expected prompt to contain section header %q, got:\n%s", header, prompt)
		}
	}
	// Check order: AGENTS before CLAUDE before GEMINI
	agentsIdx := strings.Index(prompt, "agents-content")
	claudeIdx := strings.Index(prompt, "claude-content")
	geminiIdx := strings.Index(prompt, "gemini-content")
	if !(agentsIdx < claudeIdx && claudeIdx < geminiIdx) {
		t.Fatalf("expected AGENTS.md before CLAUDE.md before GEMINI.md in prompt, got indices %d %d %d", agentsIdx, claudeIdx, geminiIdx)
	}
}

func TestExecutionPromptOmitsMissingProjectFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("only-claude"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	work := Work{
		ID:    "batch-01",
		Title: "Batch title",
		Workstreams: []Workstream{
			{Name: "01", Title: "Slice title", Summary: "do it"},
		},
	}
	prompt, err := executionPrompt(root, work, work.Workstreams[0])
	if err != nil {
		t.Fatalf("executionPrompt: %v", err)
	}
	if !strings.Contains(prompt, "only-claude") {
		t.Fatalf("expected prompt to contain CLAUDE.md content, got:\n%s", prompt)
	}
	// No AGENTS.md or GEMINI.md headers
	if strings.Contains(prompt, "## AGENTS.md") {
		t.Fatalf("expected prompt NOT to contain AGENTS.md header, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "## GEMINI.md") {
		t.Fatalf("expected prompt NOT to contain GEMINI.md header, got:\n%s", prompt)
	}
}

func TestExecutionPromptContainsAntiRecursionContract(t *testing.T) {
	work := Work{
		ID:    "batch-01",
		Title: "Batch title",
		Workstreams: []Workstream{
			{Name: "01", Title: "Slice title", Summary: "do it"},
		},
	}
	prompt, err := executionPrompt("", work, work.Workstreams[0])
	if err != nil {
		t.Fatalf("executionPrompt: %v", err)
	}
	for _, want := range []string{
		"Do NOT invoke",
		"springfield:*",
		"Do NOT run `springfield start`",
		".springfield/",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected anti-recursion contract to contain %q, got:\n%s", want, prompt)
		}
	}
}

func TestExecutionPromptIncludesBatchRequestBody(t *testing.T) {
	work := Work{
		ID:          "batch-01",
		Title:       "Batch title",
		RequestBody: "Polish status UX",
		Workstreams: []Workstream{
			{Name: "01", Title: "Slice title", Summary: "do it"},
		},
	}
	prompt, err := executionPrompt("", work, work.Workstreams[0])
	if err != nil {
		t.Fatalf("executionPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Polish status UX") {
		t.Fatalf("expected prompt to contain RequestBody, got:\n%s", prompt)
	}
}

func TestRuntimeSingleExecutorRejectsOversizedGuidanceFile(t *testing.T) {
	root := t.TempDir()
	// File exceeds the per-file 64 KB cap; error fires at read time, not after assembly.
	huge := strings.Repeat("x", maxGuidanceFileBytes+1)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(huge), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	executor := runtimeSingleExecutor{
		runner:  coreruntime.NewTestRunner(testRuntimeRegistry(), fakeRuntimeSuccess, time.Now),
		agents:  []agents.ID{agents.AgentClaude},
		workDir: t.TempDir(),
	}
	_, err := executor.Run(root, Work{
		ID:    "x",
		Title: "x",
		Split: "single",
		Workstreams: []Workstream{{Name: "01", Title: "T"}},
	})
	if err == nil {
		t.Fatal("expected error for oversized guidance file, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %q, want it to mention 'exceeds'", err)
	}
}

func TestRuntimeMultiExecutorRejectsOversizedGuidanceFile(t *testing.T) {
	root := t.TempDir()
	huge := strings.Repeat("x", maxGuidanceFileBytes+1)
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(huge), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	executor := runtimeMultiExecutor{
		runner:  coreruntime.NewTestRunner(testRuntimeRegistry(), fakeRuntimeSuccess, time.Now),
		agents:  []agents.ID{agents.AgentClaude},
		workDir: t.TempDir(),
	}
	report, err := executor.Run(root, Work{
		ID:    "x",
		Title: "x",
		Split: "parallel",
		Workstreams: []Workstream{{Name: "01", Title: "T"}},
	})
	if err == nil {
		t.Fatal("expected error for oversized guidance file, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %q, want it to mention 'exceeds'", err)
	}
	if report.Workstreams[0].Status != statusFailed {
		t.Fatalf("workstream status = %q, want %q", report.Workstreams[0].Status, statusFailed)
	}
}

func TestRuntimeSingleExecutorRejectsUnreadableGuidanceFile(t *testing.T) {
	root := t.TempDir()
	// Create a guidance file then make it unreadable (non-ENOENT error).
	guidancePath := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(guidancePath, []byte("content"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.Chmod(guidancePath, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(guidancePath, 0o644) })

	executor := runtimeSingleExecutor{
		runner:  coreruntime.NewTestRunner(testRuntimeRegistry(), fakeRuntimeSuccess, time.Now),
		agents:  []agents.ID{agents.AgentClaude},
		workDir: t.TempDir(),
	}
	_, err := executor.Run(root, Work{
		ID:    "x",
		Title: "x",
		Split: "single",
		Workstreams: []Workstream{{Name: "01", Title: "T"}},
	})
	if err == nil {
		t.Fatal("expected error for unreadable guidance file, got nil")
	}
	if !strings.Contains(err.Error(), "project guidance") {
		t.Fatalf("error = %q, want it to mention 'project guidance'", err)
	}
}

// TestRuntimeSingleExecutorPromptSizeCap verifies the total-prompt guard fires
// when the assembled prompt exceeds maxExecutionPromptBytes. The per-file cap
// (64 KB × 3 = 192 KB) cannot by itself reach 200 KB, so we trigger it via
// a large RequestBody (source.md content), which has no per-file cap.
func TestRuntimeSingleExecutorPromptSizeCap(t *testing.T) {
	executor := runtimeSingleExecutor{
		runner:  coreruntime.NewTestRunner(testRuntimeRegistry(), fakeRuntimeSuccess, time.Now),
		agents:  []agents.ID{agents.AgentClaude},
		workDir: t.TempDir(),
	}
	_, err := executor.Run("", Work{
		ID:          "x",
		Title:       "x",
		Split:       "single",
		RequestBody: strings.Repeat("x", maxExecutionPromptBytes),
		Workstreams: []Workstream{{Name: "01", Title: "T"}},
	})
	if err == nil {
		t.Fatal("expected error for oversized total prompt, got nil")
	}
	if !strings.Contains(err.Error(), "prompt too large") {
		t.Fatalf("error = %q, want it to contain 'prompt too large'", err)
	}
}
