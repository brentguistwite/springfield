// Package catalog assembles the full set of agent adapters. It is the only
// package that imports all four agent subpackages together, allowing the
// parent agents package to stay free of circular import dependencies.
package catalog

import (
	"fmt"

	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/core/agents/opencode"
)

// DefaultAdapters returns all detectable agent adapters in canonical order:
// claude, codex, gemini, opencode. All four adapters are executable — gemini
// joined in 2026-04, opencode joined in 2026-08.
//
// Capabilities are enforced here, at the assembly point, per AGENTS.md
// Principle 5: the compile-time pins beside each adapter constructor cover
// the concrete types, but only this check catches a future constructor or
// wrapper change that returns an adapter missing a capability the runtime
// discovers by type assertion — those fallbacks are silent.
func DefaultAdapters(lookPath agents.LookPathFunc) []agents.Adapter {
	adapters := []agents.Adapter{
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
		opencode.New(lookPath),
	}
	for _, a := range adapters {
		requireCapabilities(a)
	}
	return adapters
}

// requireCapabilities panics if a is missing any capability its agent must
// provide. Cooldown is claude-only by design (codex, gemini, and opencode have no
// cooldown semantics — see the runtime runner's cooldown handling). The
// default branch forces new adapters to extend this check rather than skip it.
func requireCapabilities(a agents.Adapter) {
	// Commander is required of every adapter: the runtime discovers it by
	// type assertion and refuses to run plans without it.
	if _, hasCommand := a.(agents.Commander); !hasCommand {
		panic(fmt.Sprintf("%s adapter missing required capability: Commander", a.ID()))
	}

	_, hasValidator := a.(agents.ResultValidator)
	_, hasClassifier := a.(agents.ErrorClassifier)
	_, hasModels := a.(agents.ModelProvider)
	_, hasTranscript := a.(agents.TranscriptDecoder)
	_, hasCooldown := a.(agents.Cooldowner)

	missing := func() string {
		return fmt.Sprintf(
			"%s adapter missing capability at assembly: validator=%t classifier=%t models=%t transcript=%t cooldown=%t",
			a.ID(), hasValidator, hasClassifier, hasModels, hasTranscript, hasCooldown,
		)
	}

	switch a.ID() {
	case agents.AgentClaude:
		if !hasValidator || !hasClassifier || !hasModels || !hasTranscript || !hasCooldown {
			panic(missing())
		}
	case agents.AgentCodex, agents.AgentGemini, agents.AgentOpenCode:
		if !hasValidator || !hasClassifier || !hasModels || !hasTranscript {
			panic(missing())
		}
	default:
		panic(fmt.Sprintf("unknown adapter id %q assembled in catalog; extend requireCapabilities with its required capability set", a.ID()))
	}
}
