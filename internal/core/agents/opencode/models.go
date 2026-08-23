package opencode

// One stable hosted flagship per major non-Anthropic provider, in
// "provider/model" form as opencode's --model expects. Verified against
// `opencode models` (CLI v1.18.21, models.dev catalog 2026-08-22). The
// opencode/*-prefixed Zen/free-preview slugs are deliberately excluded —
// they churn too fast to hardcode. Free-text model entry remains the
// primary path for everything else.
var suggestedModels = []string{
	"openai/gpt-5.6",
	"google/gemini-2.5-pro",
	"xai/grok-4.6",
}

// SuggestedModels returns a curated, non-exhaustive set of OpenCode CLI
// model IDs ("provider/model") that Springfield surfaces as suggestions.
// Free-text model entry remains the primary path for newly released models.
func SuggestedModels() []string {
	return append([]string(nil), suggestedModels...)
}
