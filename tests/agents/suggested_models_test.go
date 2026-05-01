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

			hasNonEmpty := false
			for _, model := range models {
				if strings.TrimSpace(model) != "" {
					hasNonEmpty = true
					break
				}
			}
			if !hasNonEmpty {
				t.Fatalf("SuggestedModels() returned only empty model ids for %q", agentID)
			}
		})
	}
}
