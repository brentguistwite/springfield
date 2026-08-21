package runtime_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
)

// passingEvents is a minimal claude transcript that satisfies the implementer
// tool-action contract, so the runner reaches a clean StatusPassed without the
// stall wiring being what fails the run.
func passingEvents() []exec.Event {
	return []exec.Event{{
		Type: exec.EventStdout,
		Data: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1"},{"type":"tool_result","tool_use_id":"t1","is_error":false}]}}`,
		Time: time.Now(),
	}}
}

// TestRunAttachesStallDetectorOnlyWhenThresholdSet proves the runner translates
// Request.StallThreshold into a live exec-layer stall monitor: a positive
// threshold attaches a detector (exec.Run will heartbeat and watch it), while a
// zero threshold leaves cmd.Stall nil so detection is fully disabled — the
// documented opt-out that carries no monitoring cost.
func TestRunAttachesStallDetectorOnlyWhenThresholdSet(t *testing.T) {
	var captured exec.Command
	runFn := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		captured = cmd
		return exec.Result{ExitCode: 0, Events: passingEvents()}
	}
	runner := coreruntime.NewTestRunner(envTestRegistry(), runFn, time.Now)

	runner.Run(context.Background(), coreruntime.Request{
		AgentIDs:       []agents.ID{agents.AgentClaude},
		Prompt:         "do the work",
		WorkDir:        t.TempDir(),
		StallThreshold: 5 * time.Minute,
		OnStall:        func() {},
	})
	if captured.Stall == nil {
		t.Fatal("StallThreshold > 0 must attach a stall monitor to the command")
	}

	captured = exec.Command{}
	runner.Run(context.Background(), coreruntime.Request{
		AgentIDs: []agents.ID{agents.AgentClaude},
		Prompt:   "do the work",
		WorkDir:  t.TempDir(),
		// StallThreshold unset (0) → detection disabled.
	})
	if captured.Stall != nil {
		t.Fatal("StallThreshold == 0 must leave the command's stall monitor nil (disabled)")
	}
}

// TestRunWiresOnStallIntoAttachedDetector proves the OnStall callback the caller
// supplies is the one the attached detector fires: an idle stretch past the
// threshold (driven here through the monitor's own Watch/Observe surface with a
// fake clock, standing in for exec.Run's live hosting) invokes exactly the
// caller's callback. This is the seam planrun escalates through.
func TestRunWiresOnStallIntoAttachedDetector(t *testing.T) {
	// Fake clock: the runner passes its clock (here, this func) into the detector,
	// so advancing it moves the detector's notion of "now" without real waiting.
	var nowNanos atomic.Int64
	nowNanos.Store(time.Unix(0, 0).UnixNano())
	clock := func() time.Time { return time.Unix(0, nowNanos.Load()) }

	var fired atomic.Int64
	var captured exec.Command
	runFn := func(_ context.Context, cmd exec.Command, _ exec.EventHandler) exec.Result {
		captured = cmd
		return exec.Result{ExitCode: 0, Events: passingEvents()}
	}
	runner := coreruntime.NewTestRunner(envTestRegistry(), runFn, clock)
	runner.Run(context.Background(), coreruntime.Request{
		AgentIDs:       []agents.ID{agents.AgentClaude},
		Prompt:         "do the work",
		WorkDir:        t.TempDir(),
		StallThreshold: 100 * time.Millisecond,
		OnStall:        func() { fired.Add(1) },
	})
	if captured.Stall == nil {
		t.Fatal("expected a stall monitor to be attached")
	}

	// Advance the fake clock well past the threshold, then let the monitor's
	// watcher observe the staleness (its poll ticker runs on real time, so a
	// short wait suffices). No Observe() call → the idle stretch stands.
	nowNanos.Store(time.Unix(0, 0).Add(time.Second).UnixNano())
	watchCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go captured.Stall.Watch(watchCtx)

	deadline := time.After(2 * time.Second)
	for fired.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("OnStall was never fired by the attached detector")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
