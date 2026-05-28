package config

// LocalFileName is the git-ignored, per-operator override file that carries
// review configuration. It sits beside springfield.toml (FileName) and is
// never committed — so a personal review prompt that references a personal
// skill never leaks to teammates who clone the repo.
const LocalFileName = "springfield.local.toml"

// DefaultMaxReviewIterations is the review fix-loop budget used when the
// operator does not set max_review_iterations.
const DefaultMaxReviewIterations = 3

// ReviewConfig is the [review] block from springfield.local.toml. The zero
// value means review is disabled (Enabled=false), matching the opt-in default.
type ReviewConfig struct {
	// Enabled turns review on for the whole project. Default false (opt-in):
	// review spawns extra (possibly cross-agent) runs and costs tokens/time.
	Enabled bool `toml:"enabled"`
	// Agent names the reviewer agent (e.g. "codex"). Empty → fall back to the
	// implementing CLI (see ReviewAgentOrImplementer). Cross-agent is the
	// recommended upgrade for independence.
	Agent string `toml:"agent"`
	// Prompt is the bring-your-own review methodology. Empty → planreview's
	// built-in default prompt (resolved in impl plan 2, not here).
	Prompt string `toml:"prompt"`
	// MaxReviewIterations caps the review fix-loop's revise→fix→re-review
	// rounds before escalating to needs-human. <=0 → DefaultMaxReviewIterations.
	MaxReviewIterations int `toml:"max_review_iterations"`
}

// MaxReviewIterationsOrDefault returns the configured cap, or
// DefaultMaxReviewIterations when unset/non-positive.
func (r ReviewConfig) MaxReviewIterationsOrDefault() int {
	if r.MaxReviewIterations <= 0 {
		return DefaultMaxReviewIterations
	}
	return r.MaxReviewIterations
}

// LocalConfig is the decoded springfield.local.toml. A missing file decodes to
// the zero value, which leaves review disabled.
type LocalConfig struct {
	Review ReviewConfig `toml:"review"`
}
