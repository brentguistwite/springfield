package cost_test

import (
	"math"
	"testing"

	"springfield/internal/features/cost"
)

func TestCompute_KnownPair(t *testing.T) {
	// Claude Sonnet 4.6: $3.00/Mtok input, $15.00/Mtok output.
	// 1_000_000 input + 500_000 output = $3.00 + $7.50 = $10.50.
	got, ok := cost.Compute("claude", "claude-sonnet-4-6", 1_000_000, 500_000)
	if !ok {
		t.Fatal("expected priced pair, got unpriced")
	}
	want := 10.50
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("Compute(claude,sonnet-4-6,1M,500K) = %v, want %v", got, want)
	}
}

func TestCompute_UnknownAdapter(t *testing.T) {
	got, ok := cost.Compute("gemini", "gemini-2.5", 1_000_000, 1_000_000)
	if ok {
		t.Fatalf("expected unknown adapter to return ok=false, got cost=%v", got)
	}
	if got != 0 {
		t.Fatalf("expected zero cost for unknown adapter, got %v", got)
	}
}

func TestCompute_UnknownModel(t *testing.T) {
	got, ok := cost.Compute("claude", "claude-future-99", 1_000_000, 1_000_000)
	if ok {
		t.Fatalf("expected unknown model to return ok=false, got cost=%v", got)
	}
	if got != 0 {
		t.Fatalf("expected zero cost for unknown model, got %v", got)
	}
}

func TestCompute_ZeroTokens(t *testing.T) {
	// Zero-token call against a priced pair: ok=true, cost=0. Distinguishes
	// real $0 iteration from unknown-pricing iteration.
	got, ok := cost.Compute("claude", "claude-sonnet-4-6", 0, 0)
	if !ok {
		t.Fatal("expected priced pair to return ok=true even at zero tokens")
	}
	if got != 0 {
		t.Fatalf("expected zero cost for zero-token call, got %v", got)
	}
}

func TestCompute_CaseInsensitive(t *testing.T) {
	got, ok := cost.Compute("CLAUDE", "Claude-Sonnet-4-6", 1_000_000, 0)
	if !ok {
		t.Fatal("expected case-insensitive lookup to succeed")
	}
	want := 3.00
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("case-insensitive Compute = %v, want %v", got, want)
	}
}

func TestCompute_CodexPair(t *testing.T) {
	// Codex gpt-5.4: $1.25/Mtok input, $10/Mtok output.
	// 200_000 input + 100_000 output = $0.25 + $1.00 = $1.25.
	got, ok := cost.Compute("codex", "gpt-5.4", 200_000, 100_000)
	if !ok {
		t.Fatal("expected gpt-5.4 to be priced")
	}
	want := 1.25
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("Compute(codex,gpt-5.4) = %v, want %v", got, want)
	}
}

func TestLookupRate_Empty(t *testing.T) {
	_, ok := cost.LookupRate("", "")
	if ok {
		t.Fatal("expected empty pair to return ok=false")
	}
}
