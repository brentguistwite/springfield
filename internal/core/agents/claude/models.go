package claude

var suggestedModels = []string{
	"claude-opus-4-7",
	"claude-sonnet-4-6",
	"claude-haiku-4-5-20251001",
	"claude-sonnet-4-5",
	"claude-opus-4-1",
}

// SuggestedModels returns a curated, non-exhaustive set of Claude CLI model
// IDs that Springfield surfaces as suggestions. Free-text model entry remains
// the primary path for newly released models.
func SuggestedModels() []string {
	return append([]string(nil), suggestedModels...)
}
