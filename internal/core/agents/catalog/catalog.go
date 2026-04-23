// Package catalog assembles the full set of agent adapters. It is the only
// package that imports all three agent subpackages together, allowing the
// parent agents package to stay free of circular import dependencies.
package catalog

import (
	"springfield/internal/core/agents"
	"springfield/internal/core/agents/claude"
	"springfield/internal/core/agents/codex"
	"springfield/internal/core/agents/gemini"
)

// DefaultAdapters returns all detectable agent adapters in canonical order:
// claude, codex, gemini. All three adapters are executable — gemini joined
// in 2026-04.
func DefaultAdapters(lookPath agents.LookPathFunc) []agents.Adapter {
	return []agents.Adapter{
		claude.New(lookPath),
		codex.New(lookPath),
		gemini.New(lookPath),
	}
}
