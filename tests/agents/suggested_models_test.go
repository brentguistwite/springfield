package agents_test

import (
	"os/exec"
	"strings"
	"testing"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
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

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
