// Package catalog assembles the full set of agent adapters. It is the only
// package that imports all four agent subpackages together, allowing the
// parent agents package to stay free of circular import dependencies.
package catalog

import (
	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
	"springfield/internal/core/agents/opencode"
)

// DefaultAdapters returns all detectable agent adapters in canonical order:
// claude, codex, gemini, opencode. All four adapters are executable — gemini
// joined in 2026-04, opencode joined in 2026-08.
func DefaultAdapters(lookPath agents.LookPathFunc) []agents.Adapter {
	return []agents.Adapter{
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
		opencode.New(lookPath),
	}
}
