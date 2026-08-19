package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

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
	// implementing CLI (the runner resolves the fallback at review time).
	// Cross-agent is the recommended upgrade for independence.
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

// NotifyConfig is the [notify] block from springfield.local.toml. The zero
// value means notifications are disabled (Enabled=false), matching the opt-in
// default. Notify targets (a webhook URL, ntfy topic, Slack hook baked into a
// command) are machine-personal, so this lives in the git-ignored local file
// beside [review] rather than the committed springfield.toml.
type NotifyConfig struct {
	// Enabled turns notifications on for terminal batch states. Default false
	// (opt-in): an unconfigured run stays silent and fires zero delivery.
	Enabled bool `toml:"enabled"`
	// Command is an optional user-supplied command run once per event for
	// webhook/ntfy/Slack delivery. It runs via `sh -c` with the event details
	// exported as SPRINGFIELD_NOTIFY_* environment variables. Empty → fall back
	// to the built-in macOS Notification Center delivery (osascript); on other
	// platforms an empty command with Enabled=true delivers nothing.
	Command string `toml:"command"`
}

// LocalConfig is the decoded springfield.local.toml. A missing file decodes to
// the zero value, which leaves review and notifications disabled.
type LocalConfig struct {
	Review ReviewConfig `toml:"review"`
	Notify NotifyConfig `toml:"notify"`
}

// LoadLocalFrom loads springfield.local.toml from rootDir. A missing file is
// NOT an error: it returns the zero LocalConfig (review disabled), so review is
// strictly opt-in. A present-but-malformed file returns an *InvalidConfigError,
// mirroring how the main loader reports a bad springfield.toml.
//
// Unknown keys are rejected. The file is tiny and operator-edited; silently
// dropping a typo like `eanbled = true` or `max_review_iteration` (missing s)
// would leave review off with no diagnostic, which is exactly the surprise
// the gate's opt-in design is meant to avoid.
//
// rootDir is the project root (Loaded.RootDir from the main config load), so
// the local override sits beside the springfield.toml it augments.
func LoadLocalFrom(rootDir string) (LocalConfig, error) {
	path := filepath.Join(rootDir, LocalFileName)
	var lc LocalConfig
	md, err := toml.DecodeFile(path, &lc)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LocalConfig{}, nil
		}
		return LocalConfig{}, &InvalidConfigError{Path: path, Reason: err.Error()}
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return LocalConfig{}, &InvalidConfigError{
			Path:   path,
			Reason: "unknown keys: " + strings.Join(keys, ", "),
		}
	}
	return lc, nil
}

// ReviewEnabledForPlan resolves whether review runs for one plan. An explicit
// per-plan flag wins over the global default in BOTH directions; a nil per-plan
// flag falls back to the global Enabled. (Design: per-plan true enables even
// when global is off; per-plan false suppresses even when globally on.)
func ReviewEnabledForPlan(global ReviewConfig, perPlan *bool) bool {
	if perPlan != nil {
		return *perPlan
	}
	return global.Enabled
}
