package runtime_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
)

// cooldownCommander stubs a Commander that optionally implements
// ErrorClassifier and Cooldowner. Used to drive runner cooldown branches
// without binding to a specific real adapter.
type cooldownCommander struct {
	id       agents.ID
	class    agents.ErrorClass
	cooldown time.Time
}

func (c *cooldownCommander) ID() agents.ID { return c.id }
func (c *cooldownCommander) Metadata() agents.Metadata {
	return agents.Metadata{ID: c.id, Name: string(c.id), Binary: string(c.id)}
}
func (c *cooldownCommander) Detect(context.Context) agents.Detection {
	return agents.Detection{ID: c.id, Status: agents.DetectionStatusAvailable}
}
func (c *cooldownCommander) Command(input agents.CommandInput) (exec.Command, error) {
	return exec.Command{Name: string(c.id), Dir: input.WorkDir}, nil
}
func (c *cooldownCommander) ClassifyError(_ []exec.Event, _ int, _ error) agents.ErrorClass {
	return c.class
}
func (c *cooldownCommander) Cooldown(_ []exec.Event, _ int, _ error, _ time.Time) time.Time {
	return c.cooldown
}

// fixedClock returns the same time on every call. fakeClock advances by 1s
// each call which makes installCooldown/getCooldown comparisons brittle.
type fixedClock struct {
	t time.Time
}

func newFixedClock(t time.Time) *fixedClock { return &fixedClock{t: t} }
func (c *fixedClock) now() time.Time        { return c.t }

func TestRunner_SkipsAgentInCooldown(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC))
	claude := &cooldownCommander{id: agents.AgentClaude, class: agents.ErrorClassRetryable}
	codex := &cooldownCommander{id: agents.AgentCodex, class: agents.ErrorClassFatal}
	registry := agents.NewRegistry(claude, codex)

	var calls []string
	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		calls = append(calls, cmd.Name)
		return exec.Result{ExitCode: 0}
	}

	r := runtime.NewTestRunner(registry, fakeRun, clock.now)
	r.SetCooldown(agents.AgentClaude, clock.now().Add(30*time.Minute))

	result := r.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:   "x",
	})

	if result.Status != runtime.StatusPassed {
		t.Fatalf("status: got %v want passed (err=%v)", result.Status, result.Err)
	}
	if result.Agent != agents.AgentCodex {
		t.Fatalf("agent: got %v want codex", result.Agent)
	}
	if len(calls) != 1 || calls[0] != string(agents.AgentCodex) {
		t.Fatalf("expected codex only, got %v", calls)
	}
}

func TestRunner_CooldownExpired_TriesAgentAgain(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC))
	claude := &cooldownCommander{id: agents.AgentClaude}
	registry := agents.NewRegistry(claude)

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		return exec.Result{ExitCode: 0}
	}

	r := runtime.NewTestRunner(registry, fakeRun, clock.now)
	r.SetCooldown(agents.AgentClaude, clock.now().Add(-1*time.Minute))

	result := r.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "x",
	})

	if result.Status != runtime.StatusPassed {
		t.Fatalf("status: got %v want passed", result.Status)
	}
}

func TestRunner_InstallsCooldownOnRetryableWithReset(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC))
	resetTime := clock.now().Add(2 * time.Hour)

	claude := &cooldownCommander{
		id:       agents.AgentClaude,
		class:    agents.ErrorClassRetryable,
		cooldown: resetTime,
	}
	codex := &cooldownCommander{id: agents.AgentCodex}
	registry := agents.NewRegistry(claude, codex)

	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		if cmd.Name == string(agents.AgentClaude) {
			line := "Claude AI usage limit reached|" + strconv.FormatInt(resetTime.Unix(), 10)
			return exec.Result{
				ExitCode: 1,
				Events:   []exec.Event{{Type: exec.EventStdout, Data: line}},
				Err:      errors.New("rate limit"),
			}
		}
		return exec.Result{ExitCode: 0}
	}

	r := runtime.NewTestRunner(registry, fakeRun, clock.now)
	_ = r.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:   "x",
	})

	got := r.GetCooldown(agents.AgentClaude)
	if !got.Equal(resetTime) {
		t.Fatalf("cooldown: got %v want %v", got, resetTime)
	}
}

func TestRunner_AppliesDefaultCooldownWhenAdapterReturnsZeroTime(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC))
	claude := &cooldownCommander{
		id:       agents.AgentClaude,
		class:    agents.ErrorClassRetryable,
		cooldown: time.Time{},
	}
	codex := &cooldownCommander{id: agents.AgentCodex}
	registry := agents.NewRegistry(claude, codex)

	fakeRun := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		if cmd.Name == string(agents.AgentClaude) {
			return exec.Result{ExitCode: 1, Err: errors.New("rate_limit_error")}
		}
		return exec.Result{ExitCode: 0}
	}

	r := runtime.NewTestRunner(registry, fakeRun, clock.now)
	_ = r.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:   "x",
	})

	got := r.GetCooldown(agents.AgentClaude)
	want := clock.now().Add(1 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("default cooldown: got %v want %v", got, want)
	}
}

func TestRunner_ClearsCooldownOnSuccess(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC))
	claude := &cooldownCommander{id: agents.AgentClaude}
	registry := agents.NewRegistry(claude)

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		return exec.Result{ExitCode: 0}
	}

	r := runtime.NewTestRunner(registry, fakeRun, clock.now)
	r.SetCooldown(agents.AgentClaude, clock.now().Add(-1*time.Hour))

	_ = r.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "x",
	})

	got := r.GetCooldown(agents.AgentClaude)
	if !got.IsZero() {
		t.Fatalf("cooldown not cleared: got %v want zero", got)
	}
}

func TestRunner_AllAgentsInCooldown_ReturnsFailure(t *testing.T) {
	clock := newFixedClock(time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC))
	claude := &cooldownCommander{id: agents.AgentClaude}
	codex := &cooldownCommander{id: agents.AgentCodex}
	registry := agents.NewRegistry(claude, codex)

	fakeRun := func(_ context.Context, _ exec.Command, _ exec.EventHandler) exec.Result {
		t.Fatal("no agent should run while all are cooled down")
		return exec.Result{}
	}

	r := runtime.NewTestRunner(registry, fakeRun, clock.now)
	r.SetCooldown(agents.AgentClaude, clock.now().Add(30*time.Minute))
	r.SetCooldown(agents.AgentCodex, clock.now().Add(30*time.Minute))

	result := r.Run(context.Background(), runtime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude, agents.AgentCodex},
		Prompt:   "x",
	})

	if result.Status != runtime.StatusFailed {
		t.Fatalf("status: got %v want failed", result.Status)
	}
	if result.Err == nil || !strings.Contains(result.Err.Error(), "cooldown") {
		t.Fatalf("err should mention cooldown, got %v", result.Err)
	}
}
