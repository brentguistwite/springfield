package claude

// The Claude CLI resolves these tier aliases to the latest model in each tier,
// so a batch configured with one picks up new releases without a Springfield
// update. They are the whole suggestion list on purpose: version-pinned ids
// would need a release every time Anthropic ships a model, and operators who
// want one can pass it to --model, which does not validate the model string.
//
// Pin a concrete id when a new release changing agent behavior mid-batch would
// be worse than running a stale model — unattended batches are the usual case,
// since nobody is watching to catch the regression.
var suggestedModels = []string{
	"fable",
	"opus",
	"sonnet",
	"haiku",
}

// SuggestedModels returns a curated, non-exhaustive set of Claude CLI model
// IDs that Springfield surfaces as suggestions. Free-text model entry remains
// the primary path for newly released models.
func SuggestedModels() []string {
	return append([]string(nil), suggestedModels...)
}
