package agents_test

import (
	"os/exec"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/features/cost"
)

func TestAdaptersImplementModelProvider(t *testing.T) {
	registry := agents.NewRegistry(
		claude.New(exec.LookPath),
		codex.New(exec.LookPath),
		gemini.New(exec.LookPath),
	)

	for _, agentID := range []agents.ID{
		agents.AgentClaude,
		agents.AgentCodex,
		agents.AgentGemini,
	} {
		t.Run(string(agentID), func(t *testing.T) {
			resolved, err := registry.Resolve(agents.ResolveInput{ProjectDefault: agentID})
			if err != nil {
				t.Fatalf("resolve adapter: %v", err)
			}

			provider, ok := resolved.Adapter.(agents.ModelProvider)
			if !ok {
				t.Fatalf("adapter %q does not implement ModelProvider", agentID)
			}

			models := provider.SuggestedModels()
			if len(models) == 0 {
				t.Fatalf("SuggestedModels() returned no models for %q", agentID)
			}

			for _, model := range models {
				trimmed := strings.TrimSpace(model)
				if trimmed == "" {
					t.Fatalf("SuggestedModels() returned empty model id for %q", agentID)
				}
				if trimmed != model {
					t.Fatalf("SuggestedModels() returned untrimmed model id %q for %q", model, agentID)
				}
			}
		})
	}
}

func TestGeminiSuggestedModelsCurated2Point5Family(t *testing.T) {
	models := gemini.SuggestedModels()

	for _, want := range []string{
		"gemini-2.5-pro",
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
	} {
		if !containsString(models, want) {
			t.Fatalf("SuggestedModels() missing %q; got %v", want, models)
		}
	}

	if containsString(models, "gemini-2.0-flash-exp") {
		t.Fatalf("SuggestedModels() still contains stale model %q; got %v", "gemini-2.0-flash-exp", models)
	}
}

// The picker offers tier aliases only. Version-pinned ids would need a release
// every time Anthropic ships a model; operators who want one pass it to
// --model, which does not validate the model string.
func TestClaudeSuggestedModelsAreTierAliasesOnly(t *testing.T) {
	got := claude.SuggestedModels()
	want := []string{"fable", "opus", "sonnet", "haiku"}

	if len(got) != len(want) {
		t.Fatalf("SuggestedModels() = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SuggestedModels() = %v, want %v", got, want)
		}
	}
}

// Anything the picker offers must resolve to a rate, or its runs land in
// Rollup.UnpricedRuns on the iterations where the CLI reports no total_cost_usd.
func TestClaudeSuggestedModelsArePriced(t *testing.T) {
	for _, model := range claude.SuggestedModels() {
		if _, ok := cost.LookupRate("claude", model); !ok {
			t.Errorf("suggested model %q has no pricing row", model)
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
