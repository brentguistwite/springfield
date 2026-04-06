package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	osexec "os/exec"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

func TestRunnerSuccessStreamsEventsAndReturnsPassedResult(t *testing.T) {
	registry := agents.NewRegistry(claude.New(osexec.LookPath))
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	var captured []exec.Event
	handler := func(e exec.Event) { captured = append(captured, e) }

	fakeRun := func(_ context.Context, cmd exec.Command, h exec.EventHandler) exec.Result {
		if cmd.Name != "claude" {
			t.Fatalf("expected binary %q, got %q", "claude", cmd.Name)
		}
		if cmd.Dir != "/tmp/project" {
			t.Fatalf("expected dir %q, got %q", "/tmp/project", cmd.Dir)
		}

		ev := exec.Event{Type: exec.EventStdout, Data: "done", Time: clock.now()}
		if h != nil {
			h(ev)
		}
		return exec.Result{ExitCode: 0, Events: []exec.Event{ev}}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentID: agents.AgentClaude,
		Prompt:  "implement feature",
		WorkDir: "/tmp/project",
		OnEvent: handler,
	})

	if result.Status != runtime.StatusPassed {
		t.Fatalf("expected passed, got %q (err: %v)", result.Status, result.Err)
	}
	if result.Agent != agents.AgentClaude {
		t.Fatalf("expected agent %q, got %q", agents.AgentClaude, result.Agent)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result.Events))
	}
	if len(captured) != 1 {
		t.Fatalf("expected handler to receive 1 event, got %d", len(captured))
	}
	if result.StartedAt.IsZero() || result.EndedAt.IsZero() {
		t.Fatal("expected non-zero timestamps")
	}
}

func TestRunnerFailureOnNonZeroExitReturnsFailedResult(t *testing.T) {
	registry := agents.NewRegistry(codex.New(osexec.LookPath))
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		return exec.Result{ExitCode: 1, Err: errors.New("process exited with code 1")}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentID: agents.AgentCodex,
		Prompt:  "fix bug",
		WorkDir: "/tmp/project",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed, got %q", result.Status)
	}
	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.ExitCode)
	}
	if result.Err == nil {
		t.Fatal("expected non-nil error")
	}
}

func TestRunnerRejectsUnsupportedAgent(t *testing.T) {
	registry := agents.NewRegistry(claude.New(osexec.LookPath))
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		t.Fatal("should not invoke run for unsupported agent")
		return exec.Result{}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentID: agents.ID("unknown"),
		Prompt:  "test",
		WorkDir: "/tmp",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed, got %q", result.Status)
	}
	if !errors.Is(result.Err, agents.ErrUnsupportedAgent) {
		t.Fatalf("expected ErrUnsupportedAgent, got %v", result.Err)
	}
}

func TestRunnerSetsTimeoutOnCommand(t *testing.T) {
	registry := agents.NewRegistry(claude.New(osexec.LookPath))
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	var gotTimeout time.Duration
	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		gotTimeout = cmd.Timeout
		return exec.Result{ExitCode: 0}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	runner.Run(context.Background(), runtime.Request{
		AgentID: agents.AgentClaude,
		Prompt:  "test",
		WorkDir: "/tmp",
		Timeout: 5 * time.Minute,
	})

	if gotTimeout != 5*time.Minute {
		t.Fatalf("expected timeout 5m, got %v", gotTimeout)
	}
}

type fakeClock struct {
	t time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{t: start}
}

func (c *fakeClock) now() time.Time {
	current := c.t
	c.t = c.t.Add(time.Second)
	return current
}
