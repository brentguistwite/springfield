package cmd

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents/opencode"
	"springfield/internal/features/cost"
	"springfield/internal/testsupport/fixtures"
)

func TestFormatSpendLine_TotalAndPerAdapter(t *testing.T) {
	r := cost.Rollup{
		TotalUSD: 1.47,
		PerAdapter: map[string]float64{
			"claude": 1.41,
			"codex":  0.06,
		},
		Iterations: 3,
	}
	got := formatSpendLine(r)
	want := "Est. API cost: $1.47 (claude $1.41, codex $0.06)"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestFormatSpendLine_UnpricedAppended(t *testing.T) {
	r := cost.Rollup{
		TotalUSD:     0.50,
		PerAdapter:   map[string]float64{"codex": 0.50},
		Iterations:   2,
		UnpricedRuns: 1,
	}
	got := formatSpendLine(r)
	re := regexp.MustCompile(`^Est\. API cost: \$0\.50 \(codex \$0\.50\) \(1 unpriced\)$`)
	if !re.MatchString(got) {
		t.Errorf("expected unpriced hint, got %q", got)
	}
}

func TestFormatSpendLine_NoBreakdown(t *testing.T) {
	r := cost.Rollup{TotalUSD: 0, PerAdapter: nil, Iterations: 1}
	got := formatSpendLine(r)
	if got != "Est. API cost: $0.00" {
		t.Errorf("expected bare total, got %q", got)
	}
}

func TestFormatTotalSpendLine_BatchEnd(t *testing.T) {
	r := cost.Rollup{
		TotalUSD: 2.50,
		PerAdapter: map[string]float64{
			"claude": 2.25,
			"codex":  0.25,
		},
		Iterations: 4,
	}
	got := formatTotalSpendLine(r)
	want := "Est. API-equivalent cost: $2.50 (claude $2.25, codex $0.25) — this is the API-rate cost; you're not charged for it if you're on a Claude/Codex subscription"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// Unpriced iterations come from more than one adapter now (gemini has no
// cost capture; opencode on a free model reports $0), so the hint stays
// neutral instead of naming gemini specifically.
func TestFormatTotalSpendLine_NeutralHintForUnpriced(t *testing.T) {
	r := cost.Rollup{
		TotalUSD:     0.10,
		PerAdapter:   map[string]float64{"claude": 0.10},
		Iterations:   2,
		UnpricedRuns: 1,
	}
	got := formatTotalSpendLine(r)
	if !regexp.MustCompile(`\(1 unpriced\)`).MatchString(got) {
		t.Errorf("expected neutral unpriced hint, got %q", got)
	}
	if strings.Contains(got, "gemini") {
		t.Errorf("hint must not name gemini specifically, got %q", got)
	}
}

// An opencode-only batch prices through opencode.ExtractCost, which trusts
// the provider-computed part.cost summed off the step_finish events (no
// pricing-table rows exist for opencode). The captured transcript ran on a
// free model reporting $0.00, so the priced branch is exercised with the
// same capture carrying a representative paid-provider cost — the field
// path is the verified fact; the magnitude is whatever the provider bills.
func TestFormatSpendLine_OpenCodeBatchFromExtractedCapture(t *testing.T) {
	events := fixtures.LoadEvents(t, filepath.Join("..", "tests", "realcaptures", "opencode", "success.jsonl"))

	capture := opencode.ExtractCost(events, "openai/gpt-5.4", time.Now())
	if capture.Adapter != "opencode" || capture.InputTokens != 16653 || capture.OutputTokens != 159 {
		t.Fatalf("unexpected capture from real transcript: %+v", capture)
	}

	free := cost.Rollup{
		TotalUSD:   capture.CostUSD,
		PerAdapter: map[string]float64{capture.Adapter: capture.CostUSD},
		Iterations: 1,
	}
	if got := formatSpendLine(free); got != "Est. API cost: $0.00" {
		t.Errorf("free-model opencode batch rendered %q, want \"Est. API cost: $0.00\"", got)
	}

	paid := capture
	paid.CostUSD = 4.20
	r := cost.Rollup{
		TotalUSD:   paid.CostUSD,
		PerAdapter: map[string]float64{paid.Adapter: paid.CostUSD},
		Iterations: 1,
	}
	want := "Est. API cost: $4.20 (opencode $4.20)"
	if got := formatSpendLine(r); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// Zero-cost-with-tokens captures land in UnpricedRuns (the rollup rule), so
// an opencode-only batch on a free model shows the neutral unpriced hint in
// both spend lines.
func TestFormatSpendLine_OpenCodeUnpricedNeutralCopy(t *testing.T) {
	events := fixtures.LoadEvents(t, filepath.Join("..", "tests", "realcaptures", "opencode", "tool-error.jsonl"))

	capture := opencode.ExtractCost(events, "opencode/x-preview-f-free", time.Now())
	unpriced := capture.CostUSD == 0 && (capture.InputTokens > 0 || capture.OutputTokens > 0)
	if !unpriced {
		t.Fatalf("expected unpriced-classified capture (tokens>0, cost 0), got %+v", capture)
	}

	r := cost.Rollup{
		TotalUSD:     0,
		PerAdapter:   map[string]float64{capture.Adapter: 0},
		Iterations:   1,
		UnpricedRuns: 1,
	}
	for _, line := range []string{formatSpendLine(r), formatTotalSpendLine(r)} {
		if !strings.Contains(line, "(1 unpriced)") {
			t.Errorf("missing neutral hint: %q", line)
		}
		if strings.Contains(line, "gemini") {
			t.Errorf("hint names gemini: %q", line)
		}
	}
}
