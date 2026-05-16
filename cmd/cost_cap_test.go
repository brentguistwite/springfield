package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
)

// TestPrintBatchStatusRendersCostCapped pins that a CostCapped run renders
// "Status: cost-capped" outside the state-guarded progress block, sibling to
// the existing FatalError print. The plan called out this design choice
// explicitly: cost-capped is a Run-level terminal state, not state-derived.
func TestPrintBatchStatusRendersCostCapped(t *testing.T) {
	var buf bytes.Buffer
	b := batch.Batch{ID: "test-1", Title: "T"}
	run := batch.Run{ActiveBatchID: "test-1", CostCapped: true}
	if err := printBatchStatus(&buf, t.TempDir(), b, run, nil); err != nil {
		t.Fatalf("printBatchStatus: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Status: cost-capped") {
		t.Errorf("expected cost-capped status, got:\n%s", out)
	}
	// Fatal error must NOT be rendered for a cost-cap-only run.
	if strings.Contains(out, "Fatal error:") {
		t.Errorf("unexpected fatal error in cost-capped status:\n%s", out)
	}
}

// TestPrintBatchStatusCostCappedSurvivesNilState ensures cost-capped is
// printed even when state is nil (rollup skipped path). The bug rejected
// in the 2026-05-15 R1 review was burying the cost-capped print inside
// printProgressBlock.
func TestPrintBatchStatusCostCappedSurvivesNilState(t *testing.T) {
	var buf bytes.Buffer
	b := batch.Batch{ID: "test-1", Title: "T"}
	run := batch.Run{ActiveBatchID: "test-1", CostCapped: true}
	_ = printBatchStatus(&buf, t.TempDir(), b, run, nil)
	if !strings.Contains(buf.String(), "Status: cost-capped") {
		t.Errorf("cost-capped must render even when state == nil, got:\n%s", buf.String())
	}
}

// TestPrintOrphanStatusOmitsSpend verifies the orphan path renders
// cost-capped without a dollar figure (evidence is gone, spend cannot
// be re-computed).
func TestPrintOrphanStatusOmitsSpend(t *testing.T) {
	var buf bytes.Buffer
	run := batch.Run{ActiveBatchID: "ghost", CostCapped: true}
	printOrphanStatus(&buf, run)
	out := buf.String()
	if !strings.Contains(out, "Status: cost-capped") {
		t.Errorf("expected cost-capped on orphan, got:\n%s", out)
	}
	// Orphan output must NOT include a $ figure for spend (evidence gone).
	if strings.Contains(out, "Spend:") {
		t.Errorf("orphan output should not contain Spend line, got:\n%s", out)
	}
}

// TestPrintBatchStatusSpendLineRenders verifies the Spend: line appears
// for a live batch with seeded evidence and skips for a fresh project.
func TestPrintBatchStatusSpendLineRenders(t *testing.T) {
	root := t.TempDir()

	// Seed a cost.json under .springfield/execution/plans/<plan>/evidence/iter-1/
	dir := filepath.Join(root, ".springfield", "execution", "plans", "p", "evidence", "iter-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	c := cost.Capture{
		Adapter:    "codex",
		Model:      "gpt-5.4",
		CostUSD:    0.42,
		CapturedAt: time.Now().UTC(),
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), data, 0o644); err != nil {
		t.Fatalf("write cost.json: %v", err)
	}

	var buf bytes.Buffer
	b := batch.Batch{ID: "test-1", Title: "T"}
	// state non-nil required to enter printSpendLine path.
	_ = printBatchStatus(&buf, root, b, batch.Run{ActiveBatchID: "test-1"}, conductor.NewState())
	out := buf.String()
	if !strings.Contains(out, "Spend:") {
		t.Errorf("expected Spend: line, got:\n%s", out)
	}
	if !strings.Contains(out, "0.42") {
		t.Errorf("expected dollar figure in spend, got:\n%s", out)
	}
}
