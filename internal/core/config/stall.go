package config

import (
	"strings"
	"time"
)

// DefaultStallThreshold is the event-recency staleness ceiling applied when
// [stall] threshold is omitted from springfield.toml. It sits between the
// per-iteration turn cap and the (much longer) subprocess wall-clock timeout, so
// a silently-wedged slice is classified possibly-wedged well before it burns the
// whole clock, while a legitimately slow-but-alive agent turn is not.
const DefaultStallThreshold = 5 * time.Minute

// StallConfig is the [stall] block from springfield.toml. Stall detection is
// project-wide behavior (a shareable operational policy, not a personal secret),
// so it lives in the committed config beside [verify], not the git-ignored local
// file. The zero value (Threshold unset) applies DefaultStallThreshold.
type StallConfig struct {
	// Threshold is the idle ceiling as a Go duration string (e.g. "5m", "90s").
	// A slice that emits no event within Threshold is classified possibly-wedged
	// (advisory; the subprocess is never signalled or killed). Resolve via
	// ThresholdOrDefault():
	//
	//   - empty/unparseable → DefaultStallThreshold
	//   - "0" (or negative)  → 0, which DISABLES detection
	//   - positive duration  → that duration
	Threshold string `toml:"threshold"`
}

// ThresholdOrDefault resolves the effective stall threshold. A return of 0 means
// detection is disabled and callers skip it entirely. Note the deliberate
// asymmetry, mirroring the turn cap: an *omitted* key defaults to
// DefaultStallThreshold, but an *explicit* "0" disables detection.
func (s StallConfig) ThresholdOrDefault() time.Duration {
	raw := strings.TrimSpace(s.Threshold)
	if raw == "" {
		return DefaultStallThreshold
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return DefaultStallThreshold
	}
	if d < 0 {
		return 0
	}
	return d
}
