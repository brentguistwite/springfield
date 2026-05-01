package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

func TestFallbackWalksPriorityOnRetryable(t *testing.T) {
	first := &classifyingCommander{id: agents.AgentClaude, class: agents.ErrorClassRetryable}
	second := &classifyingCommander{id: agents.AgentCodex, class: agents.ErrorClassFatal}
	registry := agents.NewRegistry(first, second)
	clock := newFakeClock(time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC))

	var calls []string
	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		if cmd.Name == string(first.id) {
			return exec.Result{ExitCode: 1, Err: errors.New("retryable failure")}
		}
		return exec.Result{ExitCode: 0}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{first.id, second.id},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
	})

	if result.Status != runtime.StatusPassed {
		t.Fatalf("expected passed, got %q (err: %v)", result.Status, result.Err)
	}
	if result.Agent != second.id {
		t.Fatalf("expected fallback agent %q, got %q", second.id, result.Agent)
	}
	if len(calls) != 2 || calls[0] != string(first.id) || calls[1] != string(second.id) {
		t.Fatalf("expected calls [%s %s], got %v", first.id, second.id, calls)
	}
}

func TestFallbackBubblesOnFatal(t *testing.T) {
	first := &classifyingCommander{id: agents.AgentClaude, class: agents.ErrorClassFatal}
	second := &classifyingCommander{id: agents.AgentCodex, class: agents.ErrorClassRetryable}
	registry := agents.NewRegistry(first, second)
	clock := newFakeClock(time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC))

	var calls []string
	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		return exec.Result{ExitCode: 1, Err: errors.New("fatal failure")}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{first.id, second.id},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed, got %q", result.Status)
	}
	if result.Agent != first.id {
		t.Fatalf("expected first agent %q, got %q", first.id, result.Agent)
	}
	if len(calls) != 1 || calls[0] != string(first.id) {
		t.Fatalf("expected only first call [%s], got %v", first.id, calls)
	}
}

func TestFallbackWithoutClassifierIsFatal(t *testing.T) {
	first := &plainCommander{id: agents.AgentClaude}
	second := &classifyingCommander{id: agents.AgentCodex, class: agents.ErrorClassRetryable}
	registry := agents.NewRegistry(first, second)
	clock := newFakeClock(time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC))

	var calls []string
	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		return exec.Result{ExitCode: 1, Err: errors.New("unclassified failure")}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{first.id, second.id},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed, got %q", result.Status)
	}
	if result.Agent != first.id {
		t.Fatalf("expected first agent %q, got %q", first.id, result.Agent)
	}
	if len(calls) != 1 || calls[0] != string(first.id) {
		t.Fatalf("expected only first call [%s], got %v", first.id, calls)
	}
}

type classifyingCommander struct {
	id    agents.ID
	class agents.ErrorClass
}

func (c *classifyingCommander) ID() agents.ID { return c.id }

func (c *classifyingCommander) Metadata() agents.Metadata {
	return agents.Metadata{ID: c.id, Name: string(c.id), Binary: string(c.id)}
}

func (c *classifyingCommander) Detect(context.Context) agents.Detection {
	return agents.Detection{ID: c.id, Status: agents.DetectionStatusAvailable}
}

func (c *classifyingCommander) Command(input agents.CommandInput) (exec.Command, error) {
	return exec.Command{Name: string(c.id), Dir: input.WorkDir}, nil
}

func (c *classifyingCommander) ClassifyError(_ []exec.Event, _ int, _ error) agents.ErrorClass {
	return c.class
}

type plainCommander struct {
	id agents.ID
}

func (c *plainCommander) ID() agents.ID { return c.id }

func (c *plainCommander) Metadata() agents.Metadata {
	return agents.Metadata{ID: c.id, Name: string(c.id), Binary: string(c.id)}
}

func (c *plainCommander) Detect(context.Context) agents.Detection {
	return agents.Detection{ID: c.id, Status: agents.DetectionStatusAvailable}
}

func (c *plainCommander) Command(input agents.CommandInput) (exec.Command, error) {
	return exec.Command{Name: string(c.id), Dir: input.WorkDir}, nil
}
