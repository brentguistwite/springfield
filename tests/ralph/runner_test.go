package ralph_test

import (
	"context"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
	"springfield/internal/features/ralph"
)

func fakeRunSuccess(_ context.Context, cmd exec.Command, handler exec.EventHandler) exec.Result {
	events := []exec.Event{
		{Type: exec.EventStdout, Data: "ok", Time: time.Now()},
	}
	if handler != nil {
		handler(events[0])
	}
	return exec.Result{ExitCode: 0, Events: events}
}

func fakeRunFailure(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
	return exec.Result{ExitCode: 1, Err: nil}
}

func testRegistry() agents.Registry {
	return agents.NewRegistry(
		claude.New(fakeLookPath),
		codex.New(fakeLookPath),
	)
}

func fakeLookPath(name string) (string, error) {
	return "/usr/local/bin/" + name, nil
}

func TestRuntimeExecutorPassesStoryToSharedRuntime(t *testing.T) {
	registry := testRegistry()
	runner := runtime.NewTestRunner(registry, fakeRunSuccess, time.Now)

	executor := ralph.NewRuntimeExecutor(runner, []agents.ID{agents.AgentClaude}, t.TempDir(), agents.ExecutionSettings{})

	story := ralph.Story{
		ID:          "US-001",
		Title:       "Bootstrap",
		Description: "implement bootstrap feature",
	}

	result := executor.Execute(story)
	if result.Err != nil {
		t.Fatalf("expected successful execution, got: %v", result.Err)
	}

	if result.Agent != "claude" {
		t.Fatalf("expected agent claude, got %q", result.Agent)
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	if result.Stdout != "ok" {
		t.Fatalf("expected stdout %q, got %q", "ok", result.Stdout)
	}
}

func TestRuntimeExecutorCollectsStdoutAndStderr(t *testing.T) {
	registry := testRegistry()
	fakeRun := func(_ context.Context, _ exec.Command, h exec.EventHandler) exec.Result {
		events := []exec.Event{
			{Type: exec.EventStdout, Data: "line 1", Time: time.Now()},
			{Type: exec.EventStderr, Data: "warn", Time: time.Now()},
			{Type: exec.EventStdout, Data: "line 2", Time: time.Now()},
		}
		for _, e := range events {
			if h != nil {
				h(e)
			}
		}
		return exec.Result{ExitCode: 0, Events: events}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, time.Now)
	executor := ralph.NewRuntimeExecutor(runner, []agents.ID{agents.AgentClaude}, t.TempDir(), agents.ExecutionSettings{})

	result := executor.Execute(ralph.Story{ID: "US-001", Title: "test"})
	if result.Stdout != "line 1\nline 2" {
		t.Fatalf("expected joined stdout, got %q", result.Stdout)
	}
	if result.Stderr != "warn" {
		t.Fatalf("expected stderr %q, got %q", "warn", result.Stderr)
	}
}

func TestRuntimeExecutorReturnsErrorOnFailure(t *testing.T) {
	registry := testRegistry()
	runner := runtime.NewTestRunner(registry, fakeRunFailure, time.Now)

	executor := ralph.NewRuntimeExecutor(runner, []agents.ID{agents.AgentClaude}, t.TempDir(), agents.ExecutionSettings{})

	story := ralph.Story{
		ID:          "US-001",
		Title:       "Bootstrap",
		Description: "implement bootstrap feature",
	}

	result := executor.Execute(story)
	if result.Err == nil {
		t.Fatal("expected error from failed runtime execution")
	}

	if result.Agent != "claude" {
		t.Fatalf("expected agent claude, got %q", result.Agent)
	}

	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.ExitCode)
	}
}
