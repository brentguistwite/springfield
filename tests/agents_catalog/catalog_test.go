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
