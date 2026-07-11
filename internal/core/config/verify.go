package config

import (
	"strings"
	"time"

	"springfield/internal/features/prd"
)

// DefaultMaxVerifyIterations is the verify fix-loop budget used when the
// operator does not set [verify] max_verify_iterations.
const DefaultMaxVerifyIterations = 3

// DefaultVerifyTimeout is the per-round wall-clock ceiling applied to the
// verify command when [verify] timeout is unset. A round that exceeds it is
// killed and recorded as timed_out (a failed round), matching the gate loop's
// treatment of a non-zero exit.
const DefaultVerifyTimeout = 20 * time.Minute

// VerifyConfig is the [verify] block from springfield.toml. Unlike [review]
// (which lives in the git-ignored local file because it may reference personal
// skills), verify is a team-shareable command like "go test ./..." and belongs
// in the committed config. The zero value means the gate is disabled
// (Enabled=false), matching the opt-in default: a project with no [verify]
// block keeps its prior marker-only completion behavior unchanged.
type VerifyConfig struct {
	// Enabled turns the verify gate on for the whole project. Default false
	// (opt-in): the gate spawns a real command run and a fix loop on failure.
	Enabled bool `toml:"enabled"`
	// Command is the shell command run via `sh -c` in the worktree root that
	// must exit 0 for a plan to be honored complete (e.g. "go test ./...").
	// Empty leaves the gate inert even when Enabled is true.
	Command string `toml:"command"`
	// Timeout is the per-round wall-clock ceiling as a Go duration string
	// (e.g. "20m", "90s"). Empty/unparseable → DefaultVerifyTimeout. Resolve
	// via TimeoutOrDefault().
	Timeout string `toml:"timeout"`
	// MaxVerifyIterations caps the verify fix-loop's run→fix→re-run rounds
	// before escalating to needs-human. <=0 → DefaultMaxVerifyIterations.
	MaxVerifyIterations int `toml:"max_verify_iterations"`
}

// TimeoutOrDefault returns the configured per-round timeout, or
// DefaultVerifyTimeout when the key is unset or not a valid Go duration.
func (v VerifyConfig) TimeoutOrDefault() time.Duration {
	s := strings.TrimSpace(v.Timeout)
	if s == "" {
		return DefaultVerifyTimeout
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return DefaultVerifyTimeout
	}
	return d
}

// MaxVerifyIterationsOrDefault returns the configured cap, or
// DefaultMaxVerifyIterations when unset/non-positive.
func (v VerifyConfig) MaxVerifyIterationsOrDefault() int {
	if v.MaxVerifyIterations <= 0 {
		return DefaultMaxVerifyIterations
	}
	return v.MaxVerifyIterations
}

// ResolvedVerify is the effective, plan-specific verify configuration after
// folding a per-plan override onto the global [verify] block. Callers gate on
// Enabled && Command != "" (see VerifyEnabledForPlan); Timeout and
// MaxIterations are always populated with their resolved defaults.
type ResolvedVerify struct {
	// Enabled is the resolved on/off toggle (per-plan wins over global).
	Enabled bool
	// Command is the resolved, trimmed command (per-plan wins when non-empty).
	Command string
	// Timeout is the resolved per-round ceiling (from the global block).
	Timeout time.Duration
	// MaxIterations is the resolved fix-loop budget (from the global block).
	MaxIterations int
}

// ResolveVerify folds a per-plan override onto the global [verify] block,
// per-plan winning in both directions:
//
//   - Enabled: an explicit per-plan flag (Override.Enabled != nil) wins over
//     the global default; a nil per-plan flag inherits global.Enabled.
//   - Command: a non-empty per-plan command replaces the global command; an
//     empty/whitespace per-plan command inherits the global command.
//
// Timeout and MaxIterations are not overridable per plan (the envelope override
// carries only {command, enabled}), so they always resolve from the global
// block's defaults.
func ResolveVerify(global VerifyConfig, perPlan *prd.VerifyOverride) ResolvedVerify {
	r := ResolvedVerify{
		Enabled:       global.Enabled,
		Command:       global.Command,
		Timeout:       global.TimeoutOrDefault(),
		MaxIterations: global.MaxVerifyIterationsOrDefault(),
	}
	if perPlan != nil {
		if perPlan.Enabled != nil {
			r.Enabled = *perPlan.Enabled
		}
		if strings.TrimSpace(perPlan.Command) != "" {
			r.Command = perPlan.Command
		}
	}
	r.Command = strings.TrimSpace(r.Command)
	return r
}

// VerifyEnabledForPlan reports whether the verify gate actually runs for one
// plan: the resolved toggle must be on AND a non-empty command must be present.
// The command check is what preserves the opt-in guarantee — a project (or
// plan) that flips Enabled on but configures no command stays inert rather than
// failing the gate with nothing to run.
func VerifyEnabledForPlan(global VerifyConfig, perPlan *prd.VerifyOverride) bool {
	r := ResolveVerify(global, perPlan)
	return r.Enabled && r.Command != ""
}
