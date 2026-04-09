package conductor_test

import (
	"errors"
	"testing"

	"springfield/internal/features/conductor"
)

type fakeExecutor struct {
	calls  []string
	failOn map[string]string
	agent  string
}

func (f *fakeExecutor) Execute(plan string) (conductor.ExecuteResult, error) {
	f.calls = append(f.calls, plan)
	agent := f.agent
	if agent == "" {
		agent = "claude"
	}
	result := conductor.ExecuteResult{Agent: agent}
	if message, ok := f.failOn[plan]; ok {
		result.EvidencePath = ".springfield/execution/evidence/" + plan + ".log"
		return result, errors.New(message)
	}
	return result, nil
}

func newRunner(t *testing.T, cfg *conductor.Config) (string, *conductor.Runner, *fakeExecutor) {
	t.Helper()

	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, cfg)

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	executor := &fakeExecutor{failOn: make(map[string]string)}
	return root, conductor.NewRunner(project, executor), executor
}

func TestRunNextSequentialPhase(t *testing.T) {
	_, runner, executor := newRunner(t, sequentialOnlyConfig())

	ran, done, err := runner.RunNext()
	if err != nil {
		t.Fatalf("run next: %v", err)
	}
	if done {
		t.Fatal("expected more work remaining")
	}
	if len(ran) != 1 || ran[0] != "01-bootstrap" {
		t.Fatalf("ran: got %v want [01-bootstrap]", ran)
	}
	if len(executor.calls) != 1 || executor.calls[0] != "01-bootstrap" {
		t.Fatalf("executor calls: got %v want [01-bootstrap]", executor.calls)
	}
}

func TestRunNextPersistsFailureState(t *testing.T) {
	root, runner, executor := newRunner(t, sequentialOnlyConfig())
	executor.failOn["01-bootstrap"] = "compile error"

	ran, done, err := runner.RunNext()
	if err == nil {
		t.Fatal("expected execution error")
	}
	if done {
		t.Fatal("expected incomplete schedule after failure")
	}
	if len(ran) != 1 || ran[0] != "01-bootstrap" {
		t.Fatalf("ran: got %v want [01-bootstrap]", ran)
	}

	reloaded, loadErr := conductor.LoadProject(root)
	if loadErr != nil {
		t.Fatalf("reload project: %v", loadErr)
	}
	if got := reloaded.PlanStatus("01-bootstrap"); got != conductor.StatusFailed {
		t.Fatalf("persisted status: got %q want %q", got, conductor.StatusFailed)
	}
	if got := reloaded.PlanError("01-bootstrap"); got != "compile error" {
		t.Fatalf("persisted error: got %q want compile error", got)
	}
}

func TestRunNextRetriesFailedPlanOnResume(t *testing.T) {
	_, runner, executor := newRunner(t, sequentialOnlyConfig())
	executor.failOn["01-bootstrap"] = "compile error"

	_, _, _ = runner.RunNext()
	delete(executor.failOn, "01-bootstrap")

	ran, done, err := runner.RunNext()
	if err != nil {
		t.Fatalf("resume run next: %v", err)
	}
	if done {
		t.Fatal("expected more work remaining")
	}
	if len(ran) != 1 || ran[0] != "01-bootstrap" {
		t.Fatalf("resume ran: got %v want [01-bootstrap]", ran)
	}
}

func TestRunAllCompletesSequentialPlansInOrder(t *testing.T) {
	_, runner, executor := newRunner(t, sequentialOnlyConfig())

	if err := runner.RunAll(); err != nil {
		t.Fatalf("run all: %v", err)
	}

	want := []string{"01-bootstrap", "02-config", "03-runtime"}
	if len(executor.calls) != len(want) {
		t.Fatalf("executor calls: got %v want %v", executor.calls, want)
	}
	for i := range want {
		if executor.calls[i] != want[i] {
			t.Fatalf("executor calls[%d]: got %q want %q", i, executor.calls[i], want[i])
		}
	}
}

func TestRunNextRunsWholeBatchPhase(t *testing.T) {
	_, runner, executor := newRunner(t, mixedConfig())

	if _, _, err := runner.RunNext(); err != nil {
		t.Fatalf("run next seq-1: %v", err)
	}
	if _, _, err := runner.RunNext(); err != nil {
		t.Fatalf("run next seq-2: %v", err)
	}

	ran, done, err := runner.RunNext()
	if err != nil {
		t.Fatalf("run next batch: %v", err)
	}
	if done {
		t.Fatal("expected more work after first batch")
	}
	if len(ran) != 2 || ran[0] != "batch-a" || ran[1] != "batch-b" {
		t.Fatalf("batch ran: got %v want [batch-a batch-b]", ran)
	}
	if len(executor.calls) != 4 {
		t.Fatalf("executor calls: got %v want 4 calls", executor.calls)
	}
}
