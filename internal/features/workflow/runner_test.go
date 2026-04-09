package workflow_test

import (
	"strings"
	"testing"

	"springfield/internal/features/workflow"
)

func TestRunnerRunRoutesSingleSplitToSingleExecutor(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Run one approved workstream through the single engine.",
		split:   "single",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "Execution adapter", summary: "Use the single engine boundary."},
		},
	})

	single := &fakeSingleExecutor{
		report: workflow.ExecutionReport{
			Status: "completed",
			Workstreams: []workflow.WorkstreamRun{
				{Name: "01", Status: "completed"},
			},
		},
	}
	multi := &fakeMultiExecutor{}
	runner := workflow.Runner{Single: single, Multi: multi}

	result, err := runner.Run(root, "wave-c2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got, want := single.calls, 1; got != want {
		t.Fatalf("single calls = %d, want %d", got, want)
	}
	if got := multi.calls; got != 0 {
		t.Fatalf("multi calls = %d, want 0", got)
	}
	if got, want := single.lastWork.ID, "wave-c2"; got != want {
		t.Fatalf("single work id = %q, want %q", got, want)
	}
	if got, want := result.WorkID, "wave-c2"; got != want {
		t.Fatalf("result work id = %q, want %q", got, want)
	}
	if got, want := result.Status, "completed"; got != want {
		t.Fatalf("result status = %q, want %q", got, want)
	}
}

func TestRunnerRunRoutesMultiSplitToMultiExecutor(t *testing.T) {
	root := t.TempDir()
	writeWorkflowDraft(t, root, workflowDraftFixture{
		workID:  "wave-c2",
		title:   "Unified execution surface",
		summary: "Run multiple approved workstreams through the multi engine.",
		split:   "multi",
		workstreams: []workflowDraftWorkstream{
			{name: "01", title: "CLI surface"},
			{name: "02", title: "TUI surface"},
		},
	})

	single := &fakeSingleExecutor{}
	multi := &fakeMultiExecutor{
		report: workflow.ExecutionReport{
			Status: "completed",
			Workstreams: []workflow.WorkstreamRun{
				{Name: "01", Status: "completed"},
				{Name: "02", Status: "completed"},
			},
		},
	}
	runner := workflow.Runner{Single: single, Multi: multi}

	result, err := runner.Run(root, "wave-c2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := single.calls; got != 0 {
		t.Fatalf("single calls = %d, want 0", got)
	}
	if got, want := multi.calls, 1; got != want {
		t.Fatalf("multi calls = %d, want %d", got, want)
	}
	if got, want := len(multi.lastWork.Workstreams), 2; got != want {
		t.Fatalf("multi workstreams = %d, want %d", got, want)
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
		Single: &fakeSingleExecutor{
			report: workflow.ExecutionReport{
				Status: "completed",
				Workstreams: []workflow.WorkstreamRun{
					{Name: "01", Status: "completed"},
				},
			},
		},
		Multi: &fakeMultiExecutor{},
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
		Single: &fakeSingleExecutor{},
		Multi: &fakeMultiExecutor{
			report: workflow.ExecutionReport{
				Status: "failed",
				Workstreams: []workflow.WorkstreamRun{
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

type fakeSingleExecutor struct {
	report   workflow.ExecutionReport
	err      error
	calls    int
	lastWork workflow.Work
}

func (f *fakeSingleExecutor) Run(root string, work workflow.Work) (workflow.ExecutionReport, error) {
	f.calls++
	f.lastWork = work
	return f.report, f.err
}

type fakeMultiExecutor struct {
	report   workflow.ExecutionReport
	err      error
	calls    int
	lastWork workflow.Work
}

func (f *fakeMultiExecutor) Run(root string, work workflow.Work) (workflow.ExecutionReport, error) {
	f.calls++
	f.lastWork = work
	return f.report, f.err
}
