package execution_test

import (
	"testing"

	"springfield/internal/features/execution"
)

func TestRunnerRunRoutesSingleSplitInternally(t *testing.T) {
	single := &fakeSingleExecutor{
		report: execution.Report{
			Status: "completed",
			Workstreams: []execution.WorkstreamRun{
				{Name: "01", Status: "completed"},
			},
		},
	}
	multi := &fakeMultiExecutor{}
	runner := execution.Runner{Single: single, Multi: multi}

	result, err := runner.Run("", execution.Work{
		ID:    "wave-d6",
		Title: "Execution seam",
		Split: "single",
		Workstreams: []execution.Workstream{
			{Name: "01", Title: "Adapter"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got, want := single.calls, 1; got != want {
		t.Fatalf("single calls = %d, want %d", got, want)
	}
	if got := multi.calls; got != 0 {
		t.Fatalf("multi calls = %d, want 0", got)
	}
	if got, want := result.Status, "completed"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

func TestRunnerRunRoutesMultiSplitInternally(t *testing.T) {
	single := &fakeSingleExecutor{}
	multi := &fakeMultiExecutor{
		report: execution.Report{
			Status: "completed",
			Workstreams: []execution.WorkstreamRun{
				{Name: "01", Status: "completed"},
				{Name: "02", Status: "completed"},
			},
		},
	}
	runner := execution.Runner{Single: single, Multi: multi}

	result, err := runner.Run("", execution.Work{
		ID:    "wave-d6",
		Title: "Execution seam",
		Split: "multi",
		Workstreams: []execution.Workstream{
			{Name: "01", Title: "Adapter"},
			{Name: "02", Title: "Scheduler"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := single.calls; got != 0 {
		t.Fatalf("single calls = %d, want 0", got)
	}
	if got, want := multi.calls, 1; got != want {
		t.Fatalf("multi calls = %d, want %d", got, want)
	}
	if got, want := result.Status, "completed"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

type fakeSingleExecutor struct {
	report execution.Report
	err    error
	calls  int
}

func (f *fakeSingleExecutor) Run(root string, work execution.Work) (execution.Report, error) {
	f.calls++
	return f.report, f.err
}

type fakeMultiExecutor struct {
	report execution.Report
	err    error
	calls  int
}

func (f *fakeMultiExecutor) Run(root string, work execution.Work) (execution.Report, error) {
	f.calls++
	return f.report, f.err
}
