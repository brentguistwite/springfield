package cmd

import (
	"regexp"
	"testing"

	"springfield/internal/features/cost"
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

func TestFormatTotalSpendLine_GeminiHintForUnpriced(t *testing.T) {
	r := cost.Rollup{
		TotalUSD:     0.10,
		PerAdapter:   map[string]float64{"claude": 0.10},
		Iterations:   2,
		UnpricedRuns: 1,
	}
	got := formatTotalSpendLine(r)
	if !regexp.MustCompile(`\(1 unpriced — likely gemini\)`).MatchString(got) {
		t.Errorf("expected gemini hint, got %q", got)
	}
}
