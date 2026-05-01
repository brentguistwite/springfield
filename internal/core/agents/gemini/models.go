package gemini

var suggestedModels = []string{
	"gemini-2.0-flash-exp",
	"gemini-2.5-pro",
	"gemini-2.5-flash",
}

// SuggestedModels returns a curated, non-exhaustive set of Gemini CLI model
// IDs that Springfield surfaces as suggestions. Free-text model entry remains
// the primary path for newly released models.
func SuggestedModels() []string {
	return append([]string(nil), suggestedModels...)
}
