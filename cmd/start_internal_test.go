package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/batch"
)

func TestSliceToExecutionWorkReadsSourceMd(t *testing.T) {
	root := t.TempDir()
	batchID := "batch-001"
	paths, err := batch.NewPaths(root, batchID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
		t.Fatalf("mkdir plan dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.PlanDir(), "source.md"), []byte("Original request X"), 0o644); err != nil {
		t.Fatalf("write source.md: %v", err)
	}

	b := batch.Batch{ID: batchID}
	s := batch.Slice{ID: "01", Title: "Slice title", Summary: "do something"}

	work := sliceToExecutionWork(root, b, s)
	if work.RequestBody != "Original request X" {
		t.Fatalf("RequestBody = %q, want %q", work.RequestBody, "Original request X")
	}
}

func TestSliceToExecutionWorkEmptyWhenSourceMdMissing(t *testing.T) {
	root := t.TempDir()
	batchID := "batch-001"

	b := batch.Batch{ID: batchID}
	s := batch.Slice{ID: "01", Title: "Slice title", Summary: "do something"}

	work := sliceToExecutionWork(root, b, s)
	if work.RequestBody != "" {
		t.Fatalf("RequestBody = %q, want empty string", work.RequestBody)
	}
}
