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
	if handler != nil {
		handler(exec.Event{Type: exec.EventStdout, Data: "ok", Time: time.Now()})
	}
	return exec.Result{ExitCode: 0}
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
