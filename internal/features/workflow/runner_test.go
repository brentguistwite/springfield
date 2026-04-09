package workflow_test

import (
	"strings"
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
			{name: "01", title: "CLI surface"},
			{name: "02", title: "TUI surface"},
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

func TestRunnerFailurePreservesEvidenceForDiagnose(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Diagnose should preserve workstream evidence.",
		split:   "multi",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "CLI surface"},
			{name: "02", title: "TUI surface"},
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

	diagnosis, diagErr := runner.Diagnose(root, "wave-c2")
	if diagErr != nil {
		t.Fatalf("Diagnose: %v", diagErr)
	}

	if got, want := diagnosis.Status, "failed"; got != want {
		t.Fatalf("diagnosis status = %q, want %q", got, want)
	}
	if got, want := len(diagnosis.Failures), 1; got != want {
		t.Fatalf("failures = %d, want %d", got, want)
	}
	if got, want := diagnosis.Failures[0].Workstream, "02"; got != want {
		t.Fatalf("failure workstream = %q, want %q", got, want)
	}
	if got, want := diagnosis.Failures[0].EvidencePath, ".springfield/work/wave-c2/logs/02.log"; got != want {
		t.Fatalf("evidence path = %q, want %q", got, want)
	}
	if !strings.Contains(strings.ToLower(diagnosis.NextStep), "resume") {
		t.Fatalf("expected resume guidance, got %q", diagnosis.NextStep)
	}
}

func TestSpringfieldDiagnoseSingleFailureReturnsStructuredEvidence(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-d1",
		title:   "Product polish",
		summary: "Diagnose should keep Springfield-owned evidence fields.",
		split:   "single",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Status UX"},
		},
	})

	runner := workflow.Runner{
		Executor: &fakeExecutionExecutor{
			report: execution.Report{
				Status: "failed",
				Workstreams: []execution.WorkstreamRun{
					{Name: "01", Status: "failed", Error: "agent failed", EvidencePath: ".springfield/work/wave-d1/logs/01.log"},
				},
			},
		},
	}

	if _, err := runner.Run(root, "wave-d1"); err == nil {
		t.Fatal("expected run failure")
	}

	diagnosis, err := runner.Diagnose(root, "wave-d1")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	if got, want := diagnosis.Summary, "1 Springfield workstream failed."; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if got, want := diagnosis.EvidencePath, ".springfield/work/wave-d1/logs/01.log"; got != want {
		t.Fatalf("evidence path = %q, want %q", got, want)
	}
	if got, want := diagnosis.LastError, "agent failed"; got != want {
		t.Fatalf("last error = %q, want %q", got, want)
	}
	if got, want := len(diagnosis.FailingWorkstreams), 1; got != want {
		t.Fatalf("failing workstreams = %d, want %d", got, want)
	}
	if got, want := diagnosis.FailingWorkstreams[0], "01"; got != want {
		t.Fatalf("failing workstream = %q, want %q", got, want)
	}
}

func TestSpringfieldDiagnoseMultiFailurePreservesStructuredEvidence(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-d1",
		title:   "Product polish",
		summary: "Diagnose should summarize multi-stream failures consistently.",
		split:   "multi",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Status UX"},
			{name: "02", title: "Docs cleanup"},
		},
	})

	runner := workflow.Runner{
		Executor: &fakeExecutionExecutor{
			report: execution.Report{
				Status: "failed",
				Workstreams: []execution.WorkstreamRun{
					{Name: "01", Status: "failed", Error: "status render failed", EvidencePath: ".springfield/work/wave-d1/logs/01.log"},
					{Name: "02", Status: "failed", Error: "docs render failed", EvidencePath: ".springfield/work/wave-d1/logs/02.log"},
				},
			},
		},
	}

	if _, err := runner.Run(root, "wave-d1"); err == nil {
		t.Fatal("expected run failure")
	}

	diagnosis, err := runner.Diagnose(root, "wave-d1")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	if got, want := diagnosis.Summary, "2 Springfield workstreams failed."; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if got, want := diagnosis.EvidencePath, ".springfield/work/wave-d1/logs/01.log"; got != want {
		t.Fatalf("evidence path = %q, want %q", got, want)
	}
	if got, want := diagnosis.LastError, "status render failed"; got != want {
		t.Fatalf("last error = %q, want %q", got, want)
	}
	if got, want := len(diagnosis.FailingWorkstreams), 2; got != want {
		t.Fatalf("failing workstreams = %d, want %d", got, want)
	}
	if got, want := diagnosis.FailingWorkstreams[0], "01"; got != want {
		t.Fatalf("first failing workstream = %q, want %q", got, want)
	}
	if got, want := diagnosis.FailingWorkstreams[1], "02"; got != want {
		t.Fatalf("second failing workstream = %q, want %q", got, want)
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
