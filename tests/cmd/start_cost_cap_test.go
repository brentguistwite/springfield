package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/batch"
	"springfield/internal/features/cost"
)

// seedCostJSON writes a Capture under the live evidence path for the given
// plan key + iteration so cost.ComputeRollup picks it up.
func seedCostJSON(t *testing.T, root, planKey string, iter int, c cost.Capture) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "execution", "plans", planKey, "evidence", "iter-"+strconvItoa(iter))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), data, 0o644); err != nil {
		t.Fatalf("write cost.json: %v", err)
	}
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// TestStartRejectsCostCappedResumeWithoutFlag verifies case (e) in the plan:
// resuming a cost-capped batch without --cost-cap is rejected before any
// dispatch with a documented error.
func TestStartRejectsCostCappedResumeWithoutFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "codex")

	// Manufacture a cost-capped batch: run.json with CostCapped=true and a
	// seeded cost.json so ComputeRollup returns a known spend.
	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: "b1", CostCapped: true}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	seedCostJSON(t, dir, "p", 1, cost.Capture{Adapter: "codex", Model: "gpt-5.4", CostUSD: 0.42, CapturedAt: time.Now().UTC()})

	output, err := runBinaryIn(t, bin, dir, "start")
	if err == nil {
		t.Fatalf("expected error, got success:\n%s", output)
	}
	if !strings.Contains(output, "cost-capped batch requires --cost-cap") {
		t.Errorf("expected rejection message, got:\n%s", output)
	}

	// run.json must remain CostCapped (no clobber on rejection).
	run, _, _ := batch.ReadRun(dir)
	if !run.CostCapped {
		t.Error("CostCapped state should persist when resume is rejected")
	}
}

// TestStartRejectsCostCappedResumeWithLowerCap verifies case (f): the new
// cap must be strictly greater than current spend.
func TestStartRejectsCostCappedResumeWithLowerCap(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "codex")

	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: "b1", CostCapped: true}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	seedCostJSON(t, dir, "p", 1, cost.Capture{Adapter: "codex", Model: "gpt-5.4", CostUSD: 1.00, CapturedAt: time.Now().UTC()})

	output, err := runBinaryIn(t, bin, dir, "start", "--cost-cap", "0.50")
	if err == nil {
		t.Fatalf("expected rejection, got success:\n%s", output)
	}
	if !strings.Contains(output, "not greater than current spend") {
		t.Errorf("expected lower-cap rejection message, got:\n%s", output)
	}

	run, _, _ := batch.ReadRun(dir)
	if !run.CostCapped {
		t.Error("CostCapped state should persist when resume is rejected")
	}
}

// TestStatusShowsCostCappedStatus verifies that `springfield status` on a
// cost-capped batch surfaces "Status: cost-capped" rather than "Fatal error".
func TestStatusShowsCostCappedStatus(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "codex")

	// Manufacture a cost-capped batch with a real batch.json on disk so
	// status takes the live-batch path (not orphan).
	bID := "b-capped"
	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: bID, CostCapped: true}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	paths, _ := batch.NewPaths(dir, bID)
	b := batch.Batch{ID: bID, Title: "T"}
	if err := os.MkdirAll(paths.PlanDir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bData, _ := json.MarshalIndent(b, "", "  ")
	if err := os.WriteFile(paths.BatchPath(), bData, 0o644); err != nil {
		t.Fatalf("write batch.json: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: cost-capped") {
		t.Errorf("expected cost-capped status, got:\n%s", output)
	}
	if strings.Contains(output, "Fatal error:") {
		t.Errorf("unexpected fatal error label in cost-capped status:\n%s", output)
	}
}
