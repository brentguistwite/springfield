package workflow_test

import (
	"testing"

	"springfield/internal/features/execution"
	"springfield/internal/features/workflow"
)

func TestRunnerRunRoutesThroughExecutionAdapterForSingleWork(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Run one approved workstream through the Springfield adapter.",
		split:   "single",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Execution adapter", summary: "Use the Springfield execution seam."},
		},
	})

	executor := &fakeExecutionExecutor{
		report: execution.Report{
			Status: "completed",
			Workstreams: []execution.WorkstreamRun{
				{Name: "01", Status: "completed"},
			},
		},
	}
	runner := workflow.Runner{Executor: executor}

	result, err := runner.Run(root, "wave-c2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got, want := executor.calls, 1; got != want {
		t.Fatalf("executor calls = %d, want %d", got, want)
	}
	if got, want := executor.lastWork.Split, "single"; got != want {
		t.Fatalf("work split = %q, want %q", got, want)
	}
	if got, want := result.WorkID, "wave-c2"; got != want {
		t.Fatalf("result work id = %q, want %q", got, want)
	}
	if got, want := result.Status, "completed"; got != want {
		t.Fatalf("result status = %q, want %q", got, want)
	}
}

func TestRunnerRunRoutesThroughExecutionAdapterForMultiWork(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Run multiple approved workstreams through the Springfield adapter.",
		split:   "multi",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Status surface"},
			{name: "02", title: "Resume surface"},
		},
	})

	executor := &fakeExecutionExecutor{
		report: execution.Report{
			Status: "completed",
			Workstreams: []execution.WorkstreamRun{
				{Name: "01", Status: "completed"},
				{Name: "02", Status: "completed"},
			},
		},
	}
	runner := workflow.Runner{Executor: executor}

	result, err := runner.Run(root, "wave-c2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got, want := executor.calls, 1; got != want {
		t.Fatalf("executor calls = %d, want %d", got, want)
	}
	if got, want := executor.lastWork.Split, "multi"; got != want {
		t.Fatalf("work split = %q, want %q", got, want)
	}
	if got, want := len(executor.lastWork.Workstreams), 2; got != want {
		t.Fatalf("workstreams = %d, want %d", got, want)
	}
	if got, want := result.Status, "completed"; got != want {
		t.Fatalf("result status = %q, want %q", got, want)
	}
}

func TestRunnerStatusReturnsSpringfieldOwnedView(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Status should stay Springfield-owned.",
		split:   "single",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Execution adapter"},
		},
	})

	runner := workflow.Runner{
		Executor: &fakeExecutionExecutor{
			report: execution.Report{
				Status: "completed",
				Workstreams: []execution.WorkstreamRun{
					{Name: "01", Status: "completed"},
				},
			},
		},
	}
	if _, err := runner.Run(root, "wave-c2"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	status, err := runner.Status(root, "wave-c2")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if got, want := status.WorkID, "wave-c2"; got != want {
		t.Fatalf("status work id = %q, want %q", got, want)
	}
	if got, want := status.Title, "Unified execution surface"; got != want {
		t.Fatalf("status title = %q, want %q", got, want)
	}
	if got, want := status.Split, "single"; got != want {
		t.Fatalf("status split = %q, want %q", got, want)
	}
	if got, want := status.Status, "completed"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got, want := len(status.Workstreams), 1; got != want {
		t.Fatalf("status workstreams = %d, want %d", got, want)
	}
	if got, want := status.Workstreams[0].Status, "completed"; got != want {
		t.Fatalf("workstream status = %q, want %q", got, want)
	}
}

func TestRunnerStatusPreservesFailureEvidence(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Status should preserve failing workstream evidence.",
		split:   "multi",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Status surface"},
			{name: "02", title: "Resume surface"},
		},
	})

	runner := workflow.Runner{
		Executor: &fakeExecutionExecutor{
			report: execution.Report{
				Status: "failed",
				Workstreams: []execution.WorkstreamRun{
					{Name: "01", Status: "completed"},
					{Name: "02", Status: "failed", Error: "agent failed", EvidencePath: ".springfield/work/wave-c2/logs/02.log"},
				},
			},
		},
	}

	result, err := runner.Run(root, "wave-c2")
	if err == nil {
		t.Fatal("expected run failure")
	}
	if got, want := result.Status, "failed"; got != want {
		t.Fatalf("result status = %q, want %q", got, want)
	}

	status, statusErr := runner.Status(root, "wave-c2")
	if statusErr != nil {
		t.Fatalf("Status: %v", statusErr)
	}

	if got, want := status.Status, "failed"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got, want := len(status.Workstreams), 2; got != want {
		t.Fatalf("workstreams = %d, want %d", got, want)
	}
	if got, want := status.Workstreams[1].Name, "02"; got != want {
		t.Fatalf("workstream name = %q, want %q", got, want)
	}
	if got, want := status.Workstreams[1].Status, "failed"; got != want {
		t.Fatalf("workstream status = %q, want %q", got, want)
	}
	if got, want := status.Workstreams[1].Error, "agent failed"; got != want {
		t.Fatalf("workstream error = %q, want %q", got, want)
	}
	if got, want := status.Workstreams[1].EvidencePath, ".springfield/work/wave-c2/logs/02.log"; got != want {
		t.Fatalf("evidence path = %q, want %q", got, want)
	}
}

type fakeExecutionExecutor struct {
	report   execution.Report
	err      error
	calls    int
	lastWork execution.Work
}

func (f *fakeExecutionExecutor) Run(root string, work execution.Work) (execution.Report, error) {
	f.calls++
	f.lastWork = work
	return f.report, f.err
}
