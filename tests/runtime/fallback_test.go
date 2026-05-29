package runtime_test

import (
	"context"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

// TestFallbackFiresForEveryClaudeFailureShape is the OUTCOME acceptance test
// for the classifier-blindness fixes (A2 + A3 + A4). It wires the REAL claude
// and codex adapters into the real runtime.Runner — only the subprocess exec
// is faked — and replays each real-world claude failure shape that previously
// short-circuited to Fatal. Every shape must now classify retryable so the
// runner falls through to codex (the agent_priority headline promise).
func TestFallbackFiresForEveryClaudeFailureShape(t *testing.T) {
	now := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	resetEpoch := now.Add(2 * time.Hour).Unix()

	tests := []struct {
		name         string
		claudeResult exec.Result
		wantCooldown bool
	}{
		{
			// A2: exit 0 with a truncated transcript. ValidateResult
			// synthesizes "no successful tool_result"; A3 surfaces the
			// structured rate_limit_event on stdout so the reordered
			// ClassifyError no longer bails Fatal on the clean exit.
			name: "exit0 synthesized no-successful-tool_result (scanner-bug shape)",
			claudeResult: exec.Result{
				ExitCode: 0,
				Events: []exec.Event{
					{Type: exec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"}]}}`},
					{Type: exec.EventStdout, Data: `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":` + strconv.FormatInt(resetEpoch, 10) + `}}`},
				},
			},
			wantCooldown: true,
		},
		{
			// A3: structured Anthropic API error on stdout. The narrow
			// stdout needle list trips on api_error_status.
			name: "exit1 api_error_status 429 on stdout",
			claudeResult: exec.Result{
				ExitCode: 1,
				Events: []exec.Event{
					{Type: exec.EventStdout, Data: `{"type":"result","is_error":true,"api_error_status":429,"subtype":"error"}`},
				},
				Err: errors.New("claude exited with non-zero code 1"),
			},
		},
		{
			// A4: canonical usage-limit message carrying an exact reset
			// epoch on stderr. Must classify retryable AND install the
			// parsed cooldown.
			name: "exit1 usage-limit-reached pipe-epoch on stderr",
			claudeResult: exec.Result{
				ExitCode: 1,
				Events: []exec.Event{
					{Type: exec.EventStderr, Data: "Claude AI usage limit reached|" + strconv.FormatInt(resetEpoch, 10)},
				},
				Err: errors.New("claude exited with non-zero code 1"),
			},
			wantCooldown: true,
		},
		{
			// Baseline: a recognized retryable phrase on stderr.
			name: "exit1 recognized retryable phrase on stderr",
			claudeResult: exec.Result{
				ExitCode: 1,
				Events: []exec.Event{
					{Type: exec.EventStderr, Data: "Error: 429 too many requests (rate limit)"},
				},
				Err: errors.New("claude exited with non-zero code 1"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := agents.NewRegistry(
				claude.NewWithOptions(func(string) (string, error) { return "/usr/bin/claude", nil }, claude.Options{WarnWriter: io.Discard}),
				codex.New(func(string) (string, error) { return "/usr/bin/codex", nil }),
			)

			var calls []string
			runFn := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
				calls = append(calls, cmd.Name)
				if cmd.Name == "claude" {
					return tt.claudeResult
				}
				// codex succeeds: one completed tool item satisfies its
				// positive-signal ValidateResult contract.
				return exec.Result{
					ExitCode: 0,
					Events: []exec.Event{
						{Type: exec.EventStdout, Data: `{"type":"item.completed","item":{"type":"command_execution","exit_code":0}}`},
					},
				}
			}

			runner := runtime.NewTestRunner(registry, runFn, func() time.Time { return now })
			result := runner.Run(context.Background(), runtime.Request{
				AgentIDs: []agents.ID{agents.AgentClaude, agents.AgentCodex},
				Prompt:   "test",
				WorkDir:  "/tmp/project",
			})

			if len(calls) != 2 || calls[0] != "claude" || calls[1] != "codex" {
				t.Fatalf("expected fallback chain [claude codex], got %v", calls)
			}
			if result.Status != runtime.StatusPassed {
				t.Fatalf("expected passed via codex fallback, got %q (err: %v)", result.Status, result.Err)
			}
			if result.Agent != agents.AgentCodex {
				t.Fatalf("expected winning agent codex, got %q", result.Agent)
			}
			cooldown := runner.GetCooldown(agents.AgentClaude)
			if tt.wantCooldown && cooldown.IsZero() {
				t.Fatalf("expected a cooldown installed for claude, got zero")
			}
		})
	}
}

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
