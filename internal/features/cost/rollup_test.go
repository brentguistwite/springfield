package cost_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/features/cost"
)

func seedCapture(t *testing.T, root, planKey string, iter int, c cost.Capture) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "execution", "plans", planKey, "evidence", "iter-"+itoa(iter))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), data, 0o644); err != nil {
		t.Fatalf("write cost.json: %v", err)
	}
}

func itoa(n int) string {
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

func TestComputeRollup_MissingDir(t *testing.T) {
	r, err := cost.ComputeRollup(t.TempDir(), "batch-1")
	if err != nil {
		t.Fatalf("expected no error for missing dir, got %v", err)
	}
	if r.TotalUSD != 0 || r.Iterations != 0 {
		t.Errorf("expected zero rollup, got %+v", r)
	}
}

func TestComputeRollup_SumsAcrossPlansAndIters(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	seedCapture(t, root, "plan-a", 1, cost.Capture{Adapter: "claude", Model: "claude-sonnet-4-6", CostUSD: 1.50, InputTokens: 500_000, OutputTokens: 0, CapturedAt: now})
	seedCapture(t, root, "plan-a", 2, cost.Capture{Adapter: "claude", Model: "claude-sonnet-4-6", CostUSD: 0.75, InputTokens: 250_000, OutputTokens: 0, CapturedAt: now})
	seedCapture(t, root, "plan-b", 1, cost.Capture{Adapter: "codex", Model: "gpt-5.4", CostUSD: 0.25, InputTokens: 200_000, OutputTokens: 0, CapturedAt: now})

	r, err := cost.ComputeRollup(root, "batch-1")
	if err != nil {
		t.Fatalf("ComputeRollup: %v", err)
	}
	if r.Iterations != 3 {
		t.Errorf("iterations=%d want 3", r.Iterations)
	}
	if math.Abs(r.TotalUSD-2.50) > 1e-9 {
		t.Errorf("total=%v want 2.50", r.TotalUSD)
	}
	if math.Abs(r.PerAdapter["claude"]-2.25) > 1e-9 {
		t.Errorf("claude=%v want 2.25", r.PerAdapter["claude"])
	}
	if math.Abs(r.PerAdapter["codex"]-0.25) > 1e-9 {
		t.Errorf("codex=%v want 0.25", r.PerAdapter["codex"])
	}
	if r.UnpricedRuns != 0 {
		t.Errorf("expected zero unpriced, got %d", r.UnpricedRuns)
	}
}

func TestComputeRollup_CountsUnpriced(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	// CostUSD=0 with non-zero tokens → unpriced
	seedCapture(t, root, "plan-c", 1, cost.Capture{Adapter: "gemini", Model: "gemini-2.5", CostUSD: 0, InputTokens: 1000, OutputTokens: 500, CapturedAt: now})
	// CostUSD=0 with zero tokens → real $0, not unpriced
	seedCapture(t, root, "plan-d", 1, cost.Capture{Adapter: "gemini", Model: "gemini-2.5", CostUSD: 0, InputTokens: 0, OutputTokens: 0, CapturedAt: now})

	r, _ := cost.ComputeRollup(root, "batch-1")
	if r.Iterations != 2 {
		t.Errorf("iterations=%d want 2", r.Iterations)
	}
	if r.UnpricedRuns != 1 {
		t.Errorf("unpriced=%d want 1", r.UnpricedRuns)
	}
}

func TestEstimatePerPlanUSD_Empty(t *testing.T) {
	low, high, n := cost.EstimatePerPlanUSD(t.TempDir(), 5)
	if low != 0 || high != 0 || n != 0 {
		t.Errorf("expected (0,0,0) for empty, got (%v,%v,%d)", low, high, n)
	}
}

func TestEstimatePerPlanUSD_OneEntry(t *testing.T) {
	root := t.TempDir()
	seedArchive(t, root, "batch-1", 1.0, 2) // $0.50/plan
	low, high, n := cost.EstimatePerPlanUSD(root, 5)
	if n != 1 {
		t.Errorf("n=%d want 1", n)
	}
	// mean 0.50 → 0.375..0.625
	if math.Abs(low-0.375) > 1e-9 || math.Abs(high-0.625) > 1e-9 {
		t.Errorf("range (%v,%v) want (0.375,0.625)", low, high)
	}
}

func TestEstimatePerPlanUSD_SkipsZeroTotal(t *testing.T) {
	root := t.TempDir()
	seedArchive(t, root, "batch-legacy", 0, 5) // legacy entry, no TotalUSD
	seedArchive(t, root, "batch-with-data", 2.0, 4)
	low, high, n := cost.EstimatePerPlanUSD(root, 5)
	if n != 1 {
		t.Errorf("expected only 1 entry to count, got n=%d", n)
	}
	// mean per-plan = 0.50 → 0.375..0.625
	if math.Abs(low-0.375) > 1e-9 || math.Abs(high-0.625) > 1e-9 {
		t.Errorf("range (%v,%v) want (0.375,0.625)", low, high)
	}
}

func TestEstimatePerPlanUSD_MultipleAveraged(t *testing.T) {
	root := t.TempDir()
	seedArchive(t, root, "b1", 1.0, 2) // 0.50/plan
	seedArchive(t, root, "b2", 2.0, 2) // 1.00/plan
	seedArchive(t, root, "b3", 0.5, 2) // 0.25/plan
	// mean = (0.5 + 1.0 + 0.25)/3 = 0.5833
	low, high, n := cost.EstimatePerPlanUSD(root, 5)
	if n != 3 {
		t.Errorf("n=%d want 3", n)
	}
	mean := (0.5 + 1.0 + 0.25) / 3
	if math.Abs(low-mean*0.75) > 1e-6 || math.Abs(high-mean*1.25) > 1e-6 {
		t.Errorf("range (%v,%v) want (%v,%v)", low, high, mean*0.75, mean*1.25)
	}
}

// seedArchive writes a minimal ArchiveEntry JSON to root/.springfield/archive/<batchID>.json.
// Mirrors the production wire format so the historical decoder exercises real paths.
func seedArchive(t *testing.T, root, batchID string, totalUSD float64, planCount int) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "archive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	plans := make([]map[string]string, planCount)
	for i := range plans {
		plans[i] = map[string]string{"id": "plan-" + itoa(i), "title": "t", "status": "completed"}
	}
	entry := map[string]any{
		"batch_id":    batchID,
		"title":       batchID,
		"archived_at": time.Now().UTC().Format(time.RFC3339Nano),
		"reason":      "completed",
		"plans":       plans,
		"total_usd":   totalUSD,
	}
	data, _ := json.MarshalIndent(entry, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, batchID+".json"), data, 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	// Stagger mod time so most-recent sort is deterministic.
	time.Sleep(2 * time.Millisecond)
}
