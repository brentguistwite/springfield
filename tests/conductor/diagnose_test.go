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
	if diagnosis.NextStep != "Run: springfield start" {
		t.Fatalf("next step: got %q want Springfield start guidance", diagnosis.NextStep)
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

	project.MarkCompleted("01-bootstrap", "claude", "")
	project.MarkFailed("02-config", "exit code 1", "claude", "")

	diagnosis := conductor.Diagnose(project)
	if len(diagnosis.Failures) != 1 {
		t.Fatalf("failures: got %d want 1", len(diagnosis.Failures))
	}
	if diagnosis.Failures[0].Plan != "02-config" {
		t.Fatalf("failed plan: got %q want 02-config", diagnosis.Failures[0].Plan)
	}
	if !strings.Contains(diagnosis.NextStep, "start") {
		t.Fatalf("next step: got %q want start guidance", diagnosis.NextStep)
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
		project.MarkCompleted(name, "claude", "")
	}

	diagnosis := conductor.Diagnose(project)
	if !diagnosis.Done {
		t.Fatal("expected completed project")
	}
	if diagnosis.Completed != diagnosis.Total {
		t.Fatalf("progress: got %d/%d want all complete", diagnosis.Completed, diagnosis.Total)
	}
}

func TestDiagnoseFailureIncludesEvidenceDetails(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkCompleted("01-bootstrap", "claude", "")
	project.MarkRunning("02-config")
	project.MarkFailed("02-config", "exit code 1", "codex", "/tmp/evidence/02-config.log")

	diagnosis := conductor.Diagnose(project)
	if len(diagnosis.Failures) != 1 {
		t.Fatalf("failures: got %d want 1", len(diagnosis.Failures))
	}

	f := diagnosis.Failures[0]
	if f.Agent != "codex" {
		t.Fatalf("failure agent: got %q want codex", f.Agent)
	}
	if f.EvidencePath != "/tmp/evidence/02-config.log" {
		t.Fatalf("failure evidence: got %q", f.EvidencePath)
	}
	if f.Attempts < 1 {
		t.Fatalf("failure attempts: got %d want >= 1", f.Attempts)
	}
}

func TestDiagnoseReportShowsEvidencePath(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkCompleted("01-bootstrap", "claude", "")
	project.MarkFailed("02-config", "exit code 1", "codex", "/tmp/evidence/02-config.log")

	report := conductor.Diagnose(project).Report()
	if !strings.Contains(report, "/tmp/evidence/02-config.log") {
		t.Fatalf("report missing evidence path: got %q", report)
	}
	if !strings.Contains(report, "codex") {
		t.Fatalf("report missing agent: got %q", report)
	}
}

func TestDiagnosePartialSuccessShowsCompletedPlans(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkCompleted("01-bootstrap", "claude", "")
	project.MarkFailed("02-config", "exit code 1", "claude", "")

	diagnosis := conductor.Diagnose(project)
	if diagnosis.Completed != 1 {
		t.Fatalf("completed: got %d want 1", diagnosis.Completed)
	}

	report := diagnosis.Report()
	if !strings.Contains(report, "1/3") {
		t.Fatalf("report missing partial progress: got %q", report)
	}
}

func TestDiagnoseReportOmitsEvidenceWhenEmpty(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, sequentialOnlyConfig())

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkFailed("01-bootstrap", "compile error", "claude", "")

	report := conductor.Diagnose(project).Report()
	if strings.Contains(report, "Evidence:") {
		t.Fatalf("report should not show evidence line when empty: got %q", report)
	}
}

func TestDiagnoseNoPlansUsesSpringfieldExecutionConfigWording(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeConductorConfig(t, root, &conductor.Config{})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	diagnosis := conductor.Diagnose(project)
	if got, want := diagnosis.NextStep, "No plans configured. Add plans to your Springfield execution config."; got != want {
		t.Fatalf("next step = %q, want %q", got, want)
	}
}
