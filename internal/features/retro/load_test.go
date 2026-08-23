package retro_test

import (
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/retro"
)

// TestLoad_RoundTripsWrittenReport pins the read/write contract: a report written
// by WriteReport is read back byte-faithfully by Load, including its findings.
func TestLoad_RoundTripsWrittenReport(t *testing.T) {
	batchDir := t.TempDir()
	want := &retro.Report{
		BatchID: "b1",
		Findings: []retro.Finding{
			{PatternKey: "verify-nonconvergence", Severity: "warning", PlanIDs: []string{"US-001", "US-002"}},
			{PatternKey: "iteration-cap", Severity: "warning", PlanIDs: []string{"US-003"}},
		},
	}
	if err := retro.WriteReport(batchDir, want); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	got, err := retro.Load(batchDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("Load returned nil for a written report")
	}
	if got.BatchID != "b1" || len(got.Findings) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// TestLoad_AbsentIsNotAnError confirms a batch dir with no retro.json yields
// (nil, nil): an absent report is a normal state, not a failure.
func TestLoad_AbsentIsNotAnError(t *testing.T) {
	got, err := retro.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load of absent retro.json errored: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil report for absent retro.json, got %+v", got)
	}
}

// TestLoad_CorruptErrors confirms a present-but-unparseable retro.json surfaces a
// decode error (the caller decides whether to ignore it).
func TestLoad_CorruptErrors(t *testing.T) {
	batchDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(batchDir, "retro.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt retro.json: %v", err)
	}
	if _, err := retro.Load(batchDir); err == nil {
		t.Fatal("expected decode error for corrupt retro.json")
	}
}

// TestLoad_EmptyBatchDirErrors mirrors the other IO entry points' caller-mistake
// guard.
func TestLoad_EmptyBatchDirErrors(t *testing.T) {
	if _, err := retro.Load(""); err == nil {
		t.Fatal("expected error for empty batchDir")
	}
}

// TestSummarize_PicksTopByPlanSpread proves the top pattern is the finding
// tripped by the most plans and the total counts every finding.
func TestSummarize_PicksTopByPlanSpread(t *testing.T) {
	r := &retro.Report{Findings: []retro.Finding{
		{PatternKey: "iteration-cap", PlanIDs: []string{"US-003"}},
		{PatternKey: "verify-nonconvergence", PlanIDs: []string{"US-001", "US-002"}},
	}}
	s := retro.Summarize(r)
	if s.TotalFindings != 2 {
		t.Fatalf("TotalFindings = %d, want 2", s.TotalFindings)
	}
	if s.TopPatternKey != "verify-nonconvergence" || s.TopCount != 2 {
		t.Fatalf("top = %q x%d, want verify-nonconvergence x2", s.TopPatternKey, s.TopCount)
	}
}

// TestSummarize_TiesBreakOnDeclarationOrder pins that equal plan-spreads keep the
// first finding in the classifiers' stable order.
func TestSummarize_TiesBreakOnDeclarationOrder(t *testing.T) {
	r := &retro.Report{Findings: []retro.Finding{
		{PatternKey: "iteration-cap", PlanIDs: []string{"US-001"}},
		{PatternKey: "stall-wedge", PlanIDs: []string{"US-002"}},
	}}
	if s := retro.Summarize(r); s.TopPatternKey != "iteration-cap" {
		t.Fatalf("tie top = %q, want iteration-cap (first in order)", s.TopPatternKey)
	}
}

// TestSummarize_NilOrEmptyIsZero confirms a nil report and a findings-less report
// both yield the zero Summary a caller renders as nothing.
func TestSummarize_NilOrEmptyIsZero(t *testing.T) {
	if s := retro.Summarize(nil); s.TotalFindings != 0 {
		t.Fatalf("nil report summary not zero: %+v", s)
	}
	if s := retro.Summarize(&retro.Report{}); s.TotalFindings != 0 {
		t.Fatalf("empty report summary not zero: %+v", s)
	}
}
