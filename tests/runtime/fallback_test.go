package runtime_test

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
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

// TestFallbackFiresOnTurnCapTrip pins the runtime-side B2 circuit-breaker:
// a real claude adapter that exits clean but burns more turns than the cap,
// without the caller's WorkCompleteCheck saying "done", must demote to a
// retryable failure (via the synthesized [runtime.TurnCapExceededReason]
// error) so the agent_priority chain falls through to codex. Closes the
// dogfood gap where planrun called EnforceTurnCap AFTER Runner.Run returned
// StatusPassed — the synthesized failure never reached the classifier,
// codex never fired, and a 200-turn thrash failed the iteration outright.
func TestFallbackFiresOnTurnCapTrip(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	registry := agents.NewRegistry(
		claude.NewWithOptions(func(string) (string, error) { return "/usr/bin/claude", nil }, claude.Options{WarnWriter: io.Discard}),
		codex.New(func(string) (string, error) { return "/usr/bin/codex", nil }),
	)

	// Thrash transcript: a successful tool_result satisfies ValidateResult so
	// claude's run looks "passed" from exec's perspective, then a terminal
	// result event carries num_turns=84 — well over the 40-turn cap — without
	// any completion signal. This is the exact shape the dogfood batch hit.
	claudeThrash := exec.Result{
		ExitCode: 0,
		Events: []exec.Event{
			{Type: exec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"}]}}`},
			{Type: exec.EventStdout, Data: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01","is_error":false}]}}`},
			{Type: exec.EventStdout, Data: `{"type":"result","subtype":"success","is_error":false,"num_turns":84}`},
		},
	}

	var calls []string
	runFn := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		if cmd.Name == "claude" {
			return claudeThrash
		}
		return exec.Result{
			ExitCode: 0,
			Events: []exec.Event{
				{Type: exec.EventStdout, Data: `{"type":"item.completed","item":{"type":"command_execution","exit_code":0}}`},
			},
		}
	}

	runner := runtime.NewTestRunner(registry, runFn, func() time.Time { return now })
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs:             []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:               "test",
		WorkDir:              "/tmp/project",
		MaxTurnsPerIteration: 40,
		// nil WorkCompleteCheck → treat every over-cap run as thrash.
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
	if runner.GetCooldown(agents.AgentClaude).IsZero() {
		t.Fatalf("expected claude cooldown installed after turn-cap synthesis, got zero")
	}
}

// TestTurnCapBeatsValidateResultOnSingleAgent directly pins the
// ordering contract by inspecting which error reaches classification.
// Using a single-agent chain (no codex fallback) means the final
// result.Err is exactly what was demoted from StatusPassed — so we can
// assert it carries [runtime.TurnCapExceededReason], not the validator's
// "claude exited without a successful tool_result". This catches an
// ordering regression that the multi-agent variant below CAN'T: a
// fallback test only checks the final outcome, so if cap and validator
// swapped order again the test would still pass as long as codex caught
// the (now misclassified) failure.
//
// Adversarial review round 2 Codex finding: the multi-agent test alone
// doesn't actually prove the ordering it claims to.
func TestTurnCapBeatsValidateResultOnSingleAgent(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	registry := agents.NewRegistry(
		claude.NewWithOptions(func(string) (string, error) { return "/usr/bin/claude", nil }, claude.Options{WarnWriter: io.Discard}),
	)

	// Adversarial shape: tool_use with NO paired tool_result (would fail
	// ValidateResult) + num_turns=84 over the 40-turn cap (would trip
	// EnforceTurnCap). With cap-before-validator ordering, the cap wins.
	claudeThrashBrokenTranscript := exec.Result{
		ExitCode: 0,
		Events: []exec.Event{
			{Type: exec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"}]}}`},
			{Type: exec.EventStdout, Data: `{"type":"result","subtype":"success","is_error":false,"num_turns":84}`},
		},
	}

	runFn := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		return claudeThrashBrokenTranscript
	}

	runner := runtime.NewTestRunner(registry, runFn, func() time.Time { return now })
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs:             []agents.ID{agents.AgentClaude},
		Prompt:               "test",
		WorkDir:              "/tmp/project",
		MaxTurnsPerIteration: 40,
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("expected failed (no fallback agent), got %q", result.Status)
	}
	if result.Err == nil {
		t.Fatal("expected non-nil err carrying the turn-cap tag")
	}
	if !strings.Contains(result.Err.Error(), runtime.TurnCapExceededReason) {
		t.Fatalf("expected err to carry %q (proves cap ran before ValidateResult), got %q",
			runtime.TurnCapExceededReason, result.Err.Error())
	}
	if strings.Contains(result.Err.Error(), "successful tool_result") {
		t.Fatalf("err contains ValidateResult's diagnostic — ordering regressed; got %q", result.Err.Error())
	}
}

// TestTurnCapTripsBeforeValidateResult covers the multi-agent fallback
// outcome under the cap-before-validator ordering: an over-cap clean exit
// with an ADVERSARIAL broken transcript (tool_use with no paired
// tool_result) still falls through to codex. Pairs with the single-agent
// ordering pin above — that test proves the synthesized error is the
// turn-cap one; this test proves it then drives the full fallback chain.
//
// NOTE: this transcript shape is adversarial, NOT the observed dogfood
// shape. The dogfood thrash had a successful tool_result paired with
// num_turns over the cap (covered by TestFallbackFiresOnTurnCapTrip
// above). The broken-transcript shape here is the case where ValidateResult
// would ALSO fail — without the round-1 ordering fix, ValidateResult would
// fire first and the synthesized "no successful tool_result" error would
// hit Fatal in ClassifyError, defeating fallback entirely.
func TestTurnCapTripsBeforeValidateResult(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	registry := agents.NewRegistry(
		claude.NewWithOptions(func(string) (string, error) { return "/usr/bin/claude", nil }, claude.Options{WarnWriter: io.Discard}),
		codex.New(func(string) (string, error) { return "/usr/bin/codex", nil }),
	)

	// Thrash + broken transcript: a tool_use with NO paired tool_result
	// (the truncation pattern Anthropic API errors produce), plus a
	// terminal result event with num_turns=84 over the 40-turn cap.
	// ValidateResult would synthesize "no successful tool_result"; the
	// turn-cap check must take precedence so the synthesized
	// iteration-turn-cap-exceeded error wins and the chain falls through.
	claudeThrashBrokenTranscript := exec.Result{
		ExitCode: 0,
		Events: []exec.Event{
			{Type: exec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"}]}}`},
			{Type: exec.EventStdout, Data: `{"type":"result","subtype":"success","is_error":false,"num_turns":84}`},
		},
	}

	var calls []string
	runFn := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		if cmd.Name == "claude" {
			return claudeThrashBrokenTranscript
		}
		return exec.Result{
			ExitCode: 0,
			Events: []exec.Event{
				{Type: exec.EventStdout, Data: `{"type":"item.completed","item":{"type":"command_execution","exit_code":0}}`},
			},
		}
	}

	runner := runtime.NewTestRunner(registry, runFn, func() time.Time { return now })
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs:             []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:               "test",
		WorkDir:              "/tmp/project",
		MaxTurnsPerIteration: 40,
	})

	if len(calls) != 2 || calls[0] != "claude" || calls[1] != "codex" {
		t.Fatalf("expected fallback chain [claude codex] (turn-cap must trip BEFORE ValidateResult), got %v", calls)
	}
	if result.Status != runtime.StatusPassed {
		t.Fatalf("expected passed via codex fallback, got %q (err: %v)", result.Status, result.Err)
	}
	if result.Agent != agents.AgentCodex {
		t.Fatalf("expected winning agent codex, got %q", result.Agent)
	}
	if runner.GetCooldown(agents.AgentClaude).IsZero() {
		t.Fatalf("expected claude cooldown installed after turn-cap synthesis (not ValidateResult miss), got zero")
	}
}

// TestTurnCapDefusedByWorkCompleteCheck pins the other half of the contract:
// when the caller's WorkCompleteCheck returns true, a high turn count must
// NOT be treated as thrash. A 200-turn iteration that genuinely completed
// the work is success — penalising claude AND making codex redo finished
// work would be doubly wrong.
func TestTurnCapDefusedByWorkCompleteCheck(t *testing.T) {
	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	registry := agents.NewRegistry(
		claude.NewWithOptions(func(string) (string, error) { return "/usr/bin/claude", nil }, claude.Options{WarnWriter: io.Discard}),
		codex.New(func(string) (string, error) { return "/usr/bin/codex", nil }),
	)

	claudeOverCapButDone := exec.Result{
		ExitCode: 0,
		Events: []exec.Event{
			{Type: exec.EventStdout, Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_01"}]}}`},
			{Type: exec.EventStdout, Data: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01","is_error":false}]}}`},
			{Type: exec.EventStdout, Data: `{"type":"result","subtype":"success","is_error":false,"num_turns":200}`},
		},
	}

	var calls []string
	runFn := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		return claudeOverCapButDone
	}

	runner := runtime.NewTestRunner(registry, runFn, func() time.Time { return now })
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs:             []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:               "test",
		WorkDir:              "/tmp/project",
		MaxTurnsPerIteration: 40,
		WorkCompleteCheck:    func([]exec.Event) bool { return true },
	})

	if len(calls) != 1 || calls[0] != "claude" {
		t.Fatalf("expected only claude to run (no fallback when work completed), got %v", calls)
	}
	if result.Status != runtime.StatusPassed {
		t.Fatalf("expected passed, got %q (err: %v)", result.Status, result.Err)
	}
	if result.Agent != agents.AgentClaude {
		t.Fatalf("expected winning agent claude, got %q", result.Agent)
	}
	if !runner.GetCooldown(agents.AgentClaude).IsZero() {
		t.Fatalf("expected no claude cooldown when WorkCompleteCheck defused the cap, got %v", runner.GetCooldown(agents.AgentClaude))
	}
}

// TestRunRecordsPerAgentAttemptsAndLogsFallthrough pins dogfood #8: when
// claude fails retryable and the run falls through to codex within a single
// Run, BOTH dispatches must be preserved as per-agent Attempts (so the caller
// can write iter-N/claude/ + iter-N/codex/ evidence instead of codex
// overwriting claude's), and the fallthrough decision + classification must be
// logged via OnEvent.
func TestRunRecordsPerAgentAttemptsAndLogsFallthrough(t *testing.T) {
	first := &classifyingCommander{id: agents.AgentClaude, class: agents.ErrorClassRetryable}
	second := &classifyingCommander{id: agents.AgentCodex, class: agents.ErrorClassFatal}
	registry := agents.NewRegistry(first, second)
	clock := newFakeClock(time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC))

	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		if cmd.Name == string(first.id) {
			return exec.Result{
				ExitCode: 1,
				Err:      errors.New("retryable failure"),
				Events:   []exec.Event{{Type: exec.EventStdout, Data: "CLAUDE_ATTEMPT"}},
			}
		}
		return exec.Result{
			ExitCode: 0,
			Events:   []exec.Event{{Type: exec.EventStdout, Data: "CODEX_ATTEMPT"}},
		}
	}

	var logs []string
	onEvent := func(e exec.Event) {
		if e.Type == exec.EventStderr {
			logs = append(logs, e.Data)
		}
	}

	runner := runtime.NewTestRunner(registry, fakeRun, clock.now)
	result := runner.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{first.id, second.id},
		Prompt:   "test",
		WorkDir:  "/tmp/project",
		OnEvent:  onEvent,
	})

	if len(result.Attempts) != 2 {
		t.Fatalf("expected 2 attempts (claude, codex), got %d: %+v", len(result.Attempts), result.Attempts)
	}
	a0, a1 := result.Attempts[0], result.Attempts[1]
	if a0.Agent != first.id || a0.Class != agents.ErrorClassRetryable {
		t.Fatalf("attempt[0] = agent %q class %q, want claude/retryable", a0.Agent, a0.Class)
	}
	if len(a0.Events) != 1 || a0.Events[0].Data != "CLAUDE_ATTEMPT" {
		t.Fatalf("claude attempt evidence not preserved: %+v", a0.Events)
	}
	if a1.Agent != second.id {
		t.Fatalf("attempt[1] agent = %q, want codex", a1.Agent)
	}
	if len(a1.Events) != 1 || a1.Events[0].Data != "CODEX_ATTEMPT" {
		t.Fatalf("codex attempt evidence not preserved: %+v", a1.Events)
	}
	if result.Agent != second.id || result.Status != runtime.StatusPassed {
		t.Fatalf("winning result = agent %q status %q, want codex/passed", result.Agent, result.Status)
	}

	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "claude") || !strings.Contains(joined, "retryable") {
		t.Fatalf("expected a fallthrough log line naming claude + its retryable classification, got:\n%s", joined)
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
