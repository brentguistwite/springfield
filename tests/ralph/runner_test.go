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

	executor := ralph.NewRuntimeExecutor(runner, agents.AgentClaude, t.TempDir())

	story := ralph.Story{
		ID:          "US-001",
		Title:       "Bootstrap",
		Description: "implement bootstrap feature",
	}

	err := executor.Execute(story)
	if err != nil {
		t.Fatalf("expected successful execution, got: %v", err)
	}
}

func TestRuntimeExecutorReturnsErrorOnFailure(t *testing.T) {
	registry := testRegistry()
	runner := runtime.NewTestRunner(registry, fakeRunFailure, time.Now)

	executor := ralph.NewRuntimeExecutor(runner, agents.AgentClaude, t.TempDir())

	story := ralph.Story{
		ID:          "US-001",
		Title:       "Bootstrap",
		Description: "implement bootstrap feature",
	}

	err := executor.Execute(story)
	if err == nil {
		t.Fatal("expected error from failed runtime execution")
	}
}
