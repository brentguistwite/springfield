package catalog_test

import (
	"context"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/catalog"
)

func TestDefaultAdaptersReturnsThreeAdaptersInOrder(t *testing.T) {
	// nil lookPath is fine — adapters fall back to exec.LookPath internally,
	// but we only call Metadata() here, not Detect().
	adapters := catalog.DefaultAdapters(nil)

	if len(adapters) != 3 {
		t.Fatalf("expected 3 adapters, got %d", len(adapters))
	}

	want := []agents.ID{agents.AgentClaude, agents.AgentCodex, agents.AgentGemini}
	for i, id := range want {
		got := adapters[i].ID()
		if got != id {
			t.Fatalf("adapters[%d].ID() = %q, want %q", i, got, id)
		}
	}
}

func TestDefaultAdaptersUsesProvidedLookPath(t *testing.T) {
	var called []string
	lookPath := func(binary string) (string, error) {
		called = append(called, binary)
		return "/usr/local/bin/" + binary, nil
	}

	adapters := catalog.DefaultAdapters(lookPath)
	if len(adapters) != 3 {
		t.Fatalf("expected 3 adapters, got %d", len(adapters))
	}

	// Call Detect on each adapter to exercise lookPath.
	for _, a := range adapters {
		_ = a.Detect(context.Background())
	}

	if len(called) == 0 {
		t.Fatal("lookPath was never called; expected it to be invoked during Detect")
	}

	want := []string{"claude", "codex", "gemini"}
	for _, binary := range want {
		found := false
		for _, c := range called {
			if c == binary {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("lookPath not called for binary %q; called = %v", binary, called)
		}
	}
}

// TestDefaultAdaptersCarryRequiredCapabilities pins the capability set the
// runtime discovers by type assertion (AGENTS.md Principle 5). The assembly-
// point check in catalog panics on gaps; this boundary test documents the
// expectation per agent — including codex/gemini's deliberate lack of
// Cooldowner — so a change to either side fails loudly here too.
func TestDefaultAdaptersCarryRequiredCapabilities(t *testing.T) {
	adapters := catalog.DefaultAdapters(nil)

	for _, a := range adapters {
		_, hasValidator := a.(agents.ResultValidator)
		_, hasClassifier := a.(agents.ErrorClassifier)
		_, hasModels := a.(agents.ModelProvider)
		_, hasTranscript := a.(agents.TranscriptDecoder)
		_, hasCooldown := a.(agents.Cooldowner)

		switch a.ID() {
		case agents.AgentClaude:
			if !hasValidator || !hasClassifier || !hasModels || !hasTranscript || !hasCooldown {
				t.Errorf("%s: validator=%t classifier=%t models=%t transcript=%t cooldown=%t; claude must carry all five",
					a.ID(), hasValidator, hasClassifier, hasModels, hasTranscript, hasCooldown)
			}
		case agents.AgentCodex, agents.AgentGemini:
			if !hasValidator || !hasClassifier || !hasModels || !hasTranscript {
				t.Errorf("%s: validator=%t classifier=%t models=%t transcript=%t; must carry all four",
					a.ID(), hasValidator, hasClassifier, hasModels, hasTranscript)
			}
			if hasCooldown {
				t.Errorf("%s implements Cooldowner but is documented as cooldown-free; update the capability expectations if that changed deliberately", a.ID())
			}
		default:
			t.Errorf("unexpected adapter id %q in default set", a.ID())
		}
	}
}
