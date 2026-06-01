package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/features/cost"
)

func TestWriteCostCreatesFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence", "iter-1")
	capturedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	c := cost.Capture{
		Adapter:      "claude",
		Model:        "claude-sonnet-4-6",
		InputTokens:  1200,
		OutputTokens: 550,
		CostUSD:      0.01185,
		CapturedAt:   capturedAt,
	}
	if err := WriteCost(dir, c); err != nil {
		t.Fatalf("WriteCost: %v", err)
	}
	path := filepath.Join(dir, "cost.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cost.json: %v", err)
	}
	var got cost.Capture
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode cost.json: %v", err)
	}
	if got.Adapter != "claude" || got.Model != "claude-sonnet-4-6" {
		t.Errorf("adapter/model mismatch: %+v", got)
	}
	if got.InputTokens != 1200 || got.OutputTokens != 550 {
		t.Errorf("tokens mismatch: %+v", got)
	}
	if got.CostUSD != 0.01185 {
		t.Errorf("cost mismatch: %v", got.CostUSD)
	}
	if !got.CapturedAt.Equal(capturedAt) {
		t.Errorf("captured_at mismatch: %v vs %v", got.CapturedAt, capturedAt)
	}
}

func TestWriteCostCreatesDirIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newly", "nested", "iter-7")
	if err := WriteCost(dir, cost.Capture{Adapter: "codex"}); err != nil {
		t.Fatalf("WriteCost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cost.json")); err != nil {
		t.Fatalf("expected cost.json under %s: %v", dir, err)
	}
}
