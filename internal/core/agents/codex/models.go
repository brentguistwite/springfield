package codex

var suggestedModels = []string{
	"gpt-5.4",
	"gpt-5-codex",
	"o3",
}

// SuggestedModels returns a curated, non-exhaustive set of Codex CLI model
// IDs that Springfield surfaces as suggestions. Free-text model entry remains
// the primary path for newly released models.
func SuggestedModels() []string {
	return append([]string(nil), suggestedModels...)
}
