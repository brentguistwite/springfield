package conductor_test

import (
	"strings"
	"testing"

	"springfield/internal/features/conductor"
)

func TestDiagnosePendingProject(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	diagnosis := conductor.Diagnose(project)
	if diagnosis.Completed != 0 || diagnosis.Total != 3 {
		t.Fatalf("progress: got %d/%d want 0/3", diagnosis.Completed, diagnosis.Total)
	}
	if diagnosis.Done {
		t.Fatal("expected unfinished project")
	}
	if len(diagnosis.Failures) != 0 {
		t.Fatalf("failures: got %d want 0", len(diagnosis.Failures))
	}
	if !strings.Contains(diagnosis.NextStep, "run") {
		t.Fatalf("next step: got %q want run guidance", diagnosis.NextStep)
	}
}

func TestDiagnoseFailureIncludesResumeGuidance(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkCompleted("01-bootstrap", "claude")
	project.MarkFailed("02-config", "exit code 1", "claude", "")

	diagnosis := conductor.Diagnose(project)
	if len(diagnosis.Failures) != 1 {
		t.Fatalf("failures: got %d want 1", len(diagnosis.Failures))
	}
	if diagnosis.Failures[0].Plan != "02-config" {
		t.Fatalf("failed plan: got %q want 02-config", diagnosis.Failures[0].Plan)
	}
	if !strings.Contains(diagnosis.NextStep, "resume") {
		t.Fatalf("next step: got %q want resume guidance", diagnosis.NextStep)
	}
	if !strings.Contains(diagnosis.Report(), "02-config: exit code 1") {
		t.Fatalf("report: got %q", diagnosis.Report())
	}
}

func TestDiagnoseCompleteProject(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	for _, name := range project.AllPlans() {
		project.MarkCompleted(name, "claude")
	}

	diagnosis := conductor.Diagnose(project)
	if !diagnosis.Done {
		t.Fatal("expected completed project")
	}
	if diagnosis.Completed != diagnosis.Total {
		t.Fatalf("progress: got %d/%d want all complete", diagnosis.Completed, diagnosis.Total)
	}
}
