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

// TestDefaultAdaptersCarryRequiredCapabilities defends the assembly-point
// check in catalog.requireCapabilities: if that guard is removed or weakened,
// this test fails. The authoritative expectation matrix lives in
// requireCapabilities plus the per-package compile-time pins — the missing-
// capability branches here are unreachable while the panic exists, so this
// duplicate deliberately asserts only what the panic cannot: that the
// returned values satisfy the capability interfaces at all, and codex/
// gemini's documented cooldown-free contract.
func TestDefaultAdaptersCarryRequiredCapabilities(t *testing.T) {
	adapters := catalog.DefaultAdapters(nil)

	for _, a := range adapters {
		if _, ok := a.(agents.Commander); !ok {
			t.Errorf("%s does not satisfy agents.Commander", a.ID())
		}
		switch a.ID() {
		case agents.AgentClaude:
			if _, ok := a.(agents.Cooldowner); !ok {
				t.Errorf("claude no longer implements Cooldowner; update requireCapabilities and this contract if deliberate")
			}
		case agents.AgentCodex, agents.AgentGemini:
			if _, ok := a.(agents.Cooldowner); ok {
				t.Errorf("%s implements Cooldowner but is documented as cooldown-free; update the capability expectations if that changed deliberately", a.ID())
			}
		default:
			t.Errorf("unexpected adapter id %q in default set", a.ID())
		}
	}
}
