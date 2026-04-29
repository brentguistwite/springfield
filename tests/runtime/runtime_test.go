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

		// Positive-signal contract: emit a tool_use/tool_result success
		// pair (collapsed into a single assistant content array so the
		// fixture stays one event) so ValidateResult treats the run as a
		// real completion.
		ev := exec.Event{
			Type: exec.EventStdout,
			Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"},{"type":"tool_result","tool_use_id":"toolu_01","is_error":false}]}}`,
			Time: clock.now(),
		}
		if h != nil {
			h(ev)
		}
		return exec.Result{ExitCode: 0, Events: []exec.Event{ev}}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "implement feature",
		WorkDir:  "/tmp/project",
		OnEvent:  handler,
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
		AgentIDs: []agents.ID{agents.AgentCodex},
		Prompt:   "fix bug",
		WorkDir:  "/tmp/project",
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
		AgentIDs: []agents.ID{agents.ID("unknown")},
		Prompt:   "test",
		WorkDir:  "/tmp",
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
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "test",
		WorkDir:  "/tmp",
		Timeout:  5 * time.Minute,
	})

	if gotTimeout != 5*time.Minute {
		t.Fatalf("expected timeout 5m, got %v", gotTimeout)
	}
}

func TestRunnerFallsBackToNextAgentOnRateLimit(t *testing.T) {
	registry := agents.NewRegistry(
		claude.New(osexec.LookPath),
		codex.New(osexec.LookPath),
	)
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	var calls []string
	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		if cmd.Name == "claude" {
			return exec.Result{
				ExitCode: 1,
				Err:      errors.New("rate limit exceeded"),
				Events:   []exec.Event{{Type: exec.EventStderr, Data: "429 Too Many Requests"}},
			}
		}
		// Codex fallback: emit a real work item so ValidateResult sees
		// the positive completion signal Policy A requires.
		return exec.Result{
			ExitCode: 0,
			Events: []exec.Event{
				{Type: exec.EventStdout, Data: `{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"go test","exit_code":0,"status":"completed"}}`},
				{Type: exec.EventStdout, Data: `{"type":"turn.completed"}`},
			},
		}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
	})

	if result.Status != runtime.StatusPassed {
		t.Fatalf("expected passed, got %q (err: %v)", result.Status, result.Err)
	}
	if result.Agent != agents.AgentCodex {
		t.Fatalf("expected codex to succeed, got %q", result.Agent)
	}
	if len(calls) != 2 || calls[0] != "claude" || calls[1] != "codex" {
		t.Fatalf("expected fallback order [claude codex], got %v", calls)
	}
}

func TestRunnerDoesNotFallbackOnGenericFailure(t *testing.T) {
	registry := agents.NewRegistry(
		claude.New(osexec.LookPath),
		codex.New(osexec.LookPath),
	)
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	var calls []string
	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		return exec.Result{ExitCode: 1, Err: errors.New("syntax error")}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed, got %q", result.Status)
	}
	if result.Agent != agents.AgentClaude {
		t.Fatalf("expected first agent to be reported, got %q", result.Agent)
	}
	if len(calls) != 1 {
		t.Fatalf("expected no fallback after generic failure, got %v", calls)
	}
}

func TestRunnerUsesReorderedPriority(t *testing.T) {
	registry := agents.NewRegistry(
		claude.New(osexec.LookPath),
		codex.New(osexec.LookPath),
	)
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	var calls []string
	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		if cmd.Name == "codex" {
			return exec.Result{ExitCode: 1, Err: errors.New("rate limit exceeded")}
		}
		// Claude success: emit a tool_use/tool_result success pair so
		// ValidateResult sees a positive completion signal.
		return exec.Result{
			ExitCode: 0,
			Events: []exec.Event{
				{Type: exec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"},{"type":"tool_result","tool_use_id":"toolu_01","is_error":false}]}}`},
			},
		}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentCodex, agents.AgentClaude},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
	})

	if result.Agent != agents.AgentClaude {
		t.Fatalf("expected fallback to claude, got %q", result.Agent)
	}
	if len(calls) != 2 || calls[0] != "codex" || calls[1] != "claude" {
		t.Fatalf("expected reordered calls [codex claude], got %v", calls)
	}
}

func TestRunnerPassesClaudeExecutionSettingsIntoCommand(t *testing.T) {
	adapter := &recordingCommander{id: agents.AgentClaude}
	registry := agents.NewRegistry(adapter)
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		return exec.Result{ExitCode: 0}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
		ExecutionSettings: agents.ExecutionSettings{
			Claude: agents.ClaudeExecutionSettings{PermissionMode: "bypassPermissions"},
		},
	})

	if got := adapter.lastInput.ExecutionSettings.Claude.PermissionMode; got != "bypassPermissions" {
		t.Fatalf("expected claude permission_mode bypassPermissions, got %q", got)
	}
}

func TestRunnerPassesCodexExecutionSettingsIntoCommand(t *testing.T) {
	adapter := &recordingCommander{id: agents.AgentCodex}
	registry := agents.NewRegistry(adapter)
	clock := newFakeClock(time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC))

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		return exec.Result{ExitCode: 0}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentCodex},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
		ExecutionSettings: agents.ExecutionSettings{
			Codex: agents.CodexExecutionSettings{
				SandboxMode:    "workspace-write",
				ApprovalPolicy: "on-request",
			},
		},
	})

	if got := adapter.lastInput.ExecutionSettings.Codex.SandboxMode; got != "workspace-write" {
		t.Fatalf("expected codex sandbox_mode workspace-write, got %q", got)
	}
	if got := adapter.lastInput.ExecutionSettings.Codex.ApprovalPolicy; got != "on-request" {
		t.Fatalf("expected codex approval_policy on-request, got %q", got)
	}
}

func TestRunnerCallsResultValidatorOnExitZero(t *testing.T) {
	validating := &validatingCommander{
		id:            agents.AgentClaude,
		validateError: errors.New("agent asked questions instead of completing work"),
	}
	registry := agents.NewRegistry(validating)
	clock := newFakeClock(time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC))

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		return exec.Result{ExitCode: 0, Events: []exec.Event{
			{Type: exec.EventStdout, Data: "some output"},
		}}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "Test story",
		WorkDir:  "/tmp/project",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed after validator rejection, got %q", result.Status)
	}
	if result.Err == nil || result.Err.Error() != "agent asked questions instead of completing work" {
		t.Fatalf("expected validator error, got: %v", result.Err)
	}
}

func TestRunnerSkipsValidatorOnNonZeroExit(t *testing.T) {
	validating := &validatingCommander{
		id:            agents.AgentClaude,
		validateError: errors.New("should not be called"),
	}
	registry := agents.NewRegistry(validating)
	clock := newFakeClock(time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC))

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		return exec.Result{ExitCode: 1, Err: errors.New("process failed")}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "test",
		WorkDir:  "/tmp",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed, got %q", result.Status)
	}
	if validating.validateCalled {
		t.Fatal("validator should not be called on non-zero exit")
	}
}

func TestRunnerPropagatesCommanderError(t *testing.T) {
	buildErr := errors.New("sentinel build failure")
	stub := &erroringCommander{id: agents.AgentGemini, buildErr: buildErr}
	registry := agents.NewRegistry(stub)
	clock := newFakeClock(time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC))

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		t.Fatal("run should not be called when Command returns error")
		return exec.Result{}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentGemini},
		Prompt:   "test",
		WorkDir:  "/tmp",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed, got %q", result.Status)
	}
	if result.Err == nil || !errors.Is(result.Err, buildErr) {
		t.Fatalf("expected err wrapping sentinel, got %v", result.Err)
	}
}

type erroringCommander struct {
	id       agents.ID
	buildErr error
}

func (c *erroringCommander) ID() agents.ID { return c.id }
func (c *erroringCommander) Metadata() agents.Metadata {
	return agents.Metadata{ID: c.id, Name: string(c.id), Binary: string(c.id)}
}
func (c *erroringCommander) Detect(context.Context) agents.Detection {
	return agents.Detection{ID: c.id, Status: agents.DetectionStatusAvailable}
}
func (c *erroringCommander) Command(_ agents.CommandInput) (exec.Command, error) {
	return exec.Command{}, c.buildErr
}

type validatingCommander struct {
	id             agents.ID
	validateError  error
	validateCalled bool
}

func (c *validatingCommander) ID() agents.ID       { return c.id }
func (c *validatingCommander) Metadata() agents.Metadata {
	return agents.Metadata{ID: c.id, Name: string(c.id), Binary: string(c.id)}
}
func (c *validatingCommander) Detect(context.Context) agents.Detection {
	return agents.Detection{ID: c.id, Status: agents.DetectionStatusAvailable}
}
func (c *validatingCommander) Command(input agents.CommandInput) (exec.Command, error) {
	return exec.Command{Name: string(c.id), Dir: input.WorkDir}, nil
}
func (c *validatingCommander) ValidateResult(result exec.Result) error {
	c.validateCalled = true
	return c.validateError
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

type recordingCommander struct {
	id        agents.ID
	lastInput agents.CommandInput
}

func (c *recordingCommander) ID() agents.ID {
	return c.id
}

func (c *recordingCommander) Metadata() agents.Metadata {
	return agents.Metadata{
		ID:     c.id,
		Name:   string(c.id),
		Binary: string(c.id),
	}
}

func (c *recordingCommander) Detect(context.Context) agents.Detection {
	return agents.Detection{ID: c.id, Status: agents.DetectionStatusAvailable}
}

func (c *recordingCommander) Command(input agents.CommandInput) (exec.Command, error) {
	c.lastInput = input
	return exec.Command{Name: string(c.id), Dir: input.WorkDir}, nil
}
