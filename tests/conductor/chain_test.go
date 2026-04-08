package conductor_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/features/conductor"
)

// Full-chain integration: conductor.Runner → RuntimeExecutor → runtime.Runner → fakeCommandFunc
// Verifies real invocation path end-to-end with hermetic doubles.

func captureCommandFunc(calls *[]exec.Command, exitCode int) exec.CommandFunc {
	return func(_ context.Context, cmd exec.Command, handler exec.EventHandler) exec.Result {
		*calls = append(*calls, cmd)
		if handler != nil {
			handler(exec.Event{Type: exec.EventStdout, Data: "agent output", Time: time.Now()})
		}
		return exec.Result{ExitCode: exitCode}
	}
}

func TestFullChainConductorRunSuccessRecordsAgent(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	cfg := sequentialOnlyConfig()
	cfg.Sequential = []string{"01-bootstrap"}
	writeConductorConfig(t, root, cfg)

	plansDir := root + "/" + cfg.PlansDir
	writePlanFile(t, plansDir, "01-bootstrap", "implement bootstrap feature")

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	var calls []exec.Command
	runner := newTestRuntime("claude", captureCommandFunc(&calls, 0))
	executor := conductor.NewRuntimeExecutor(runner, []agents.ID{"claude"}, plansDir, root)
	conductorRunner := conductor.NewRunner(project, executor)

	if err := conductorRunner.RunAll(); err != nil {
		t.Fatalf("run all: %v", err)
	}

	// Verify the command was actually constructed and dispatched
	if len(calls) != 1 {
		t.Fatalf("expected 1 command dispatch, got %d", len(calls))
	}

	// Verify command carries the plan content as prompt
	if !strings.Contains(strings.Join(calls[0].Args, " "), "bootstrap") {
		t.Fatalf("expected plan content in command args, got: %v", calls[0].Args)
	}

	// Verify state records the agent
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.PlanStatus("01-bootstrap"); got != conductor.StatusCompleted {
		t.Fatalf("plan status: got %q want completed", got)
	}
	if got := reloaded.PlanAgent("01-bootstrap"); got != "claude" {
		t.Fatalf("plan agent: got %q want claude", got)
	}
}

func TestFullChainConductorRunFailurePersistsState(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	cfg := sequentialOnlyConfig()
	cfg.Sequential = []string{"01-bootstrap", "02-config"}
	writeConductorConfig(t, root, cfg)

	plansDir := root + "/" + cfg.PlansDir
	writePlanFile(t, plansDir, "01-bootstrap", "implement bootstrap")
	writePlanFile(t, plansDir, "02-config", "implement config")

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	var calls []exec.Command
	runner := newTestRuntime("claude", captureCommandFunc(&calls, 1)) // non-zero = failure
	executor := conductor.NewRuntimeExecutor(runner, []agents.ID{"claude"}, plansDir, root)
	conductorRunner := conductor.NewRunner(project, executor)

	runErr := conductorRunner.RunAll()
	if runErr == nil {
		t.Fatal("expected failure from non-zero exit")
	}

	// Only first plan should have been attempted
	if len(calls) != 1 {
		t.Fatalf("expected 1 command dispatch (stops at first failure), got %d", len(calls))
	}

	// State should reflect failure
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.PlanStatus("01-bootstrap"); got != conductor.StatusFailed {
		t.Fatalf("plan status: got %q want failed", got)
	}
	if got := reloaded.PlanStatus("02-config"); got != conductor.StatusPending {
		t.Fatalf("second plan status: got %q want pending", got)
	}
}

func TestFullChainConductorResumeSkipsCompleted(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)

	cfg := sequentialOnlyConfig()
	writeConductorConfig(t, root, cfg)

	plansDir := root + "/" + cfg.PlansDir
	writePlanFile(t, plansDir, "01-bootstrap", "implement bootstrap")
	writePlanFile(t, plansDir, "02-config", "implement config")
	writePlanFile(t, plansDir, "03-runtime", "implement runtime")

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	// Complete first plan
	var calls []exec.Command
	runFn := captureCommandFunc(&calls, 0)
	runner := newTestRuntime("claude", runFn)
	executor := conductor.NewRuntimeExecutor(runner, []agents.ID{"claude"}, plansDir, root)
	conductorRunner := conductor.NewRunner(project, executor)

	conductorRunner.RunNext()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call after first phase, got %d", len(calls))
	}

	// Simulate resume: reload project, create new runner, continue
	project2, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	calls = nil
	runner2 := newTestRuntime("claude", captureCommandFunc(&calls, 0))
	executor2 := conductor.NewRuntimeExecutor(runner2, []agents.ID{"claude"}, plansDir, root)
	resumeRunner := conductor.NewRunner(project2, executor2)

	if err := resumeRunner.RunAll(); err != nil {
		t.Fatalf("resume run all: %v", err)
	}

	// Should have run plans 02 and 03 (not 01 again)
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls on resume, got %d", len(calls))
	}
}
