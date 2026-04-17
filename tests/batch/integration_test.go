package batch_test

import (
	"testing"

	"springfield/internal/features/batch"
)

func TestBranchTarget_BatchMode(t *testing.T) {
	b := batch.Batch{ID: "my-batch", IntegrationMode: batch.IntegrationBatch}
	target, err := batch.BranchTarget(b, "01")
	if err != nil {
		t.Fatalf("BranchTarget: %v", err)
	}
	if target != "feature/my-batch" {
		t.Errorf("BranchTarget = %q, want feature/my-batch", target)
	}
}

func TestBranchTarget_StandaloneMode(t *testing.T) {
	b := batch.Batch{ID: "my-batch", IntegrationMode: batch.IntegrationStandalone}
	target, err := batch.BranchTarget(b, "02")
	if err != nil {
		t.Fatalf("BranchTarget: %v", err)
	}
	if target != "feature/my-batch-02" {
		t.Errorf("BranchTarget = %q, want feature/my-batch-02", target)
	}
}

func TestBranchTarget_MainMode(t *testing.T) {
	b := batch.Batch{ID: "my-batch", IntegrationMode: batch.IntegrationMain}
	target, err := batch.BranchTarget(b, "01")
	if err != nil {
		t.Fatalf("BranchTarget: %v", err)
	}
	if target != "main" {
		t.Errorf("BranchTarget = %q, want main", target)
	}
}

func TestBranchTarget_StandaloneMissingSliceID(t *testing.T) {
	b := batch.Batch{ID: "my-batch", IntegrationMode: batch.IntegrationStandalone}
	_, err := batch.BranchTarget(b, "")
	if err == nil {
		t.Fatal("expected error for standalone mode with empty slice id")
	}
}

func TestSliceBranchName(t *testing.T) {
	b := batch.Batch{ID: "my-batch"}
	got := batch.SliceBranchName(b, "01")
	if got != "springfield/my-batch/01" {
		t.Errorf("SliceBranchName = %q, want springfield/my-batch/01", got)
	}
}

func TestCompileWithIntegrationStandalone(t *testing.T) {
	out, err := batch.Compile(batch.CompileInput{
		Title:       "my feature",
		Source:      "build something",
		Kind:        batch.SourcePrompt,
		Integration: batch.IntegrationStandalone,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.IntegrationMode != batch.IntegrationStandalone {
		t.Errorf("integration mode = %q, want standalone", out.Batch.IntegrationMode)
	}
}

func TestRunBatchSerialOnly(t *testing.T) {
	// Verify that a batch with no explicit parallel phases runs all slices in PhaseSerial.
	out, err := batch.Compile(batch.CompileInput{
		Title:  "serial batch",
		Source: "# Plan\n## Task 1: First\n## Task 2: Second\n",
		Kind:   batch.SourceFile,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Batch.Phases) != 1 {
		t.Fatalf("expected 1 phase (serial), got %d", len(out.Batch.Phases))
	}
	if out.Batch.Phases[0].Mode != batch.PhaseSerial {
		t.Errorf("phase mode = %q, want serial", out.Batch.Phases[0].Mode)
	}
	if len(out.Batch.Phases[0].Slices) != 2 {
		t.Errorf("phase slice count = %d, want 2", len(out.Batch.Phases[0].Slices))
	}
}
