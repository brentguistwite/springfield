package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/batch"
)

func TestPlanToExecutionWorkReadsSourceMd(t *testing.T) {
	root := t.TempDir()

	b := batch.Batch{ID: "batch-xyz", PlanIDs: []string{"plan-01"}}
	paths, err := batch.NewPaths(root, b.ID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	if err := os.WriteFile(paths.SourcePath(), []byte("Implement plan-01"), 0o644); err != nil {
		t.Fatalf("write source.md: %v", err)
	}

	work := planToExecutionWork(root, b, "plan-01")
	if work.ID != "plan-01" {
		t.Fatalf("work.ID = %q, want plan-01", work.ID)
	}
	if work.RequestBody != "Implement plan-01" {
		t.Fatalf("work.RequestBody = %q, want %q", work.RequestBody, "Implement plan-01")
	}
}

func TestPlanToExecutionWorkEmptyWhenSourceMdMissing(t *testing.T) {
	root := t.TempDir()

	b := batch.Batch{ID: "batch-xyz", PlanIDs: []string{"plan-01"}}

	work := planToExecutionWork(root, b, "plan-01")
	if work.ID != "plan-01" {
		t.Fatalf("work.ID = %q, want plan-01", work.ID)
	}
	if work.RequestBody != "" {
		t.Fatalf("work.RequestBody = %q, want empty when source.md missing", work.RequestBody)
	}
	// Title should be the plan ID when source missing
	wantTitle := filepath.Base("plan-01")
	if work.Title != wantTitle {
		t.Fatalf("work.Title = %q, want %q", work.Title, wantTitle)
	}
}
