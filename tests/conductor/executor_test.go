package conductor_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/exec"
	"springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
)

func fakeCommandFunc(exitCode int, err error) exec.CommandFunc {
	return func(_ context.Context, _ exec.Command, handler exec.EventHandler) exec.Result {
		if handler != nil {
			handler(exec.Event{Type: exec.EventStdout, Data: "output line", Time: time.Now()})
		}
		return exec.Result{ExitCode: exitCode, Err: err}
	}
}

type fakeAdapter struct {
	id agents.ID
}

func (f *fakeAdapter) ID() agents.ID { return f.id }
func (f *fakeAdapter) Metadata() agents.Metadata {
	return agents.Metadata{ID: f.id, Name: string(f.id)}
}
func (f *fakeAdapter) Detect(_ context.Context) agents.Detection { return agents.Detection{ID: f.id} }
func (f *fakeAdapter) Command(input agents.CommandInput) exec.Command {
	return exec.Command{Name: string(f.id), Args: []string{input.Prompt}, Dir: input.WorkDir}
}

func newTestRuntime(agent agents.ID, runFn exec.CommandFunc) runtime.Runner {
	adapter := &fakeAdapter{id: agent}
	registry := agents.NewRegistry(adapter)
	return runtime.NewTestRunner(registry, runFn, time.Now)
}

func writePlanFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
}

func TestRuntimeExecutorSuccess(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	writePlanFile(t, plansDir, "01-bootstrap", "implement bootstrap feature")

	runner := newTestRuntime("claude", fakeCommandFunc(0, nil))
	executor := conductor.NewRuntimeExecutor(runner, []agents.ID{"claude"}, plansDir, t.TempDir(), agents.ExecutionSettings{})

	result, err := executor.Execute("01-bootstrap")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Agent != "claude" {
		t.Fatalf("agent: got %q want claude", result.Agent)
	}
}

func TestRuntimeExecutorFailure(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	writePlanFile(t, plansDir, "01-bootstrap", "implement bootstrap feature")

	runner := newTestRuntime("claude", fakeCommandFunc(1, nil))
	executor := conductor.NewRuntimeExecutor(runner, []agents.ID{"claude"}, plansDir, t.TempDir(), agents.ExecutionSettings{})

	result, err := executor.Execute("01-bootstrap")
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if result.Agent != "claude" {
		t.Fatalf("agent on failure: got %q want claude", result.Agent)
	}
}

func TestRuntimeExecutorMissingPlanFile(t *testing.T) {
	plansDir := filepath.Join(t.TempDir(), "plans")
	runner := newTestRuntime("claude", fakeCommandFunc(0, nil))
	executor := conductor.NewRuntimeExecutor(runner, []agents.ID{"claude"}, plansDir, t.TempDir(), agents.ExecutionSettings{})

	_, err := executor.Execute("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing plan file")
	}
}

func TestRuntimeExecutorFallsBackToLegacyLocalPlans(t *testing.T) {
	root := t.TempDir()
	plansDir := filepath.Join(root, ".springfield", "execution", "plans")
	legacyPlansDir := filepath.Join(root, ".springfield", "conductor", "plans")
	writePlanFile(t, legacyPlansDir, "01-bootstrap", "implement bootstrap feature")

	runner := newTestRuntime("claude", fakeCommandFunc(0, nil))
	executor := conductor.NewRuntimeExecutor(runner, []agents.ID{"claude"}, plansDir, root, agents.ExecutionSettings{})

	result, err := executor.Execute("01-bootstrap")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Agent != "claude" {
		t.Fatalf("agent: got %q want claude", result.Agent)
	}
}

func TestRunnerPassesAgentOnSuccess(t *testing.T) {
	_, runner, _ := newRunner(t, sequentialOnlyConfig())

	_, _, err := runner.RunNext()
	if err != nil {
		t.Fatalf("run next: %v", err)
	}

	if got := runner.Project.PlanAgent("01-bootstrap"); got != "claude" {
		t.Fatalf("plan agent: got %q want claude", got)
	}
}

func TestRunnerPassesAgentAndEvidenceOnFailure(t *testing.T) {
	root, runner, executor := newRunner(t, sequentialOnlyConfig())
	executor.failOn["01-bootstrap"] = "compile error"

	_, _, _ = runner.RunNext()

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.PlanAgent("01-bootstrap"); got != "claude" {
		t.Fatalf("plan agent on failure: got %q want claude", got)
	}
	if got := reloaded.PlanEvidencePath("01-bootstrap"); got == "" {
		t.Fatal("expected evidence path on failure")
	}
}

func TestResumeSkipsCompletedAndContinues(t *testing.T) {
	_, runner, executor := newRunner(t, sequentialOnlyConfig())

	// Complete first plan
	if _, _, err := runner.RunNext(); err != nil {
		t.Fatalf("run first: %v", err)
	}

	// Verify second plan runs next
	ran, _, err := runner.RunNext()
	if err != nil {
		t.Fatalf("run second: %v", err)
	}
	if len(ran) != 1 || ran[0] != "02-config" {
		t.Fatalf("resume ran: got %v want [02-config]", ran)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("total calls: got %d want 2", len(executor.calls))
	}
}

func TestRunAllResumeAfterFailure(t *testing.T) {
	_, runner, executor := newRunner(t, sequentialOnlyConfig())
	executor.failOn["02-config"] = "timeout"

	// First RunAll fails at 02-config
	if err := runner.RunAll(); err == nil {
		t.Fatal("expected failure")
	}

	// Fix the failure, resume should continue from 02-config
	delete(executor.failOn, "02-config")
	executor.calls = nil

	if err := runner.RunAll(); err != nil {
		t.Fatalf("resume run all: %v", err)
	}

	// Should have run 02-config and 03-runtime (not 01-bootstrap again)
	if len(executor.calls) != 2 || executor.calls[0] != "02-config" || executor.calls[1] != "03-runtime" {
		t.Fatalf("resume calls: got %v want [02-config 03-runtime]", executor.calls)
	}
}
