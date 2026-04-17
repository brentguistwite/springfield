package execution

import (
	"context"
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
	events := []exec.Event{
		{Type: exec.EventStdout, Data: "ok", Time: time.Now()},
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

	cmdLine := strings.Join(append([]string{calls[0].Name}, calls[0].Args...), " ")
	for _, want := range []string{"Implement the runtime seam.", "Adapter", "Route one workstream."} {
		if !strings.Contains(cmdLine, want) {
			t.Fatalf("expected command to contain %q, got %s", want, cmdLine)
		}
	}
}

func TestRuntimeSingleExecutorRunReturnsFailedReportOnRuntimeFailure(t *testing.T) {
	registry := testRuntimeRegistry()
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
	if !strings.Contains(err.Error(), "exactly one workstream") {
		t.Fatalf("error = %q, want exact-one guard", err)
	}
}

func TestCollectRuntimeOutputJoinsStdoutAndStderr(t *testing.T) {
	stdout, stderr := collectRuntimeOutput([]exec.Event{
		{Type: exec.EventStdout, Data: "line 1"},
		{Type: exec.EventStderr, Data: "warn"},
		{Type: exec.EventStdout, Data: "line 2"},
	})

	if stdout != "line 1\nline 2" {
		t.Fatalf("stdout = %q, want %q", stdout, "line 1\nline 2")
	}
	if stderr != "warn" {
		t.Fatalf("stderr = %q, want %q", stderr, "warn")
	}
}
