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

func TestLookupRate_ClaudeTiers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   string
		wantIn  float64
		wantOut float64
	}{
		{"fable alias", "fable", 10.00, 50.00},
		{"opus alias", "opus", 5.00, 25.00},
		{"sonnet alias", "sonnet", 3.00, 15.00},
		{"haiku alias", "haiku", 1.00, 5.00},

		{"pinned fable", "claude-fable-5", 10.00, 50.00},
		{"pinned opus", "claude-opus-5", 5.00, 25.00},
		{"pinned sonnet", "claude-sonnet-5", 3.00, 15.00},
		{"dated haiku", "claude-haiku-4-5-20251001", 1.00, 5.00},

		// Models that do not exist yet. These are the cases that decide whether
		// shipping a new Claude model needs a Springfield release: the family
		// prefix has to carry them without anyone adding a row.
		{"unreleased opus", "claude-opus-99", 5.00, 25.00},
		{"unreleased sonnet", "claude-sonnet-12-3-20990101", 3.00, 15.00},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cost.LookupRate("claude", tc.model)
			if !ok {
				t.Fatalf("LookupRate(claude, %q) unpriced", tc.model)
			}
			if got.InputUSDPerMtok != tc.wantIn || got.OutputUSDPerMtok != tc.wantOut {
				t.Fatalf("LookupRate(claude, %q) = %v/%v, want %v/%v",
					tc.model, got.InputUSDPerMtok, got.OutputUSDPerMtok, tc.wantIn, tc.wantOut)
			}
		})
	}
}

// An unrecognized tier must stay unpriced rather than guess. A wrong number is
// worse than a flagged UnpricedRun, which the operator can see and act on.
func TestLookupRate_UnknownClaudeTierUnpriced(t *testing.T) {
	for _, model := range []string{
		"claude-quantum-1", // plausible-looking but unknown tier
		"claude",           // bare vendor name, no tier
		"opusplan",         // real CLI alias Springfield deliberately does not offer
	} {
		if _, ok := cost.LookupRate("claude", model); ok {
			t.Errorf("LookupRate(claude, %q) priced; want unpriced", model)
		}
	}
}

// The family fallback is claude-only: codex model ids carry no tier prefix, so
// an unknown one must not inherit a rate from a similarly-named sibling.
func TestLookupRate_CodexUnaffectedByFamilyFallback(t *testing.T) {
	if _, ok := cost.LookupRate("codex", "gpt-5.4"); !ok {
		t.Error("LookupRate(codex, gpt-5.4) unpriced; want exact-row hit")
	}
	if _, ok := cost.LookupRate("codex", "gpt-5.9"); ok {
		t.Error("LookupRate(codex, gpt-5.9) priced; want unpriced")
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
	// Codex gpt-5.4: $2.50/Mtok input, $15/Mtok output.
	// 200_000 input + 100_000 output = $0.50 + $1.50 = $2.00.
	got, ok := cost.Compute("codex", "gpt-5.4", 200_000, 100_000)
	if !ok {
		t.Fatal("expected gpt-5.4 to be priced")
	}
	want := 2.00
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
