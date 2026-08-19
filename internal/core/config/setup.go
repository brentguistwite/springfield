package config

import (
	"strings"
	"time"
)

// DefaultSetupTimeout is the wall-clock ceiling applied to the worktree setup
// command when [setup] timeout is unset. A setup that exceeds it is killed and
// recorded as timed_out (a failed setup), matching the verify gate's treatment
// of a non-zero exit. Sized generously because setup routinely installs
// dependencies (e.g. `npm install`) which can be slow on a cold cache.
const DefaultSetupTimeout = 20 * time.Minute

// SetupConfig is the [setup] block from springfield.toml. Like [verify] (and
// unlike [review], which lives in the git-ignored local file because it may
// reference personal skills), setup is a team-shareable command like
// "npm install" or "cp .env.example .env" and belongs in the committed config:
// every teammate's slice worktree needs the same preparation. The zero value
// means setup is disabled (Enabled=false), matching the opt-in default: a
// project with no [setup] block keeps its prior create-and-dispatch behavior
// byte-identical.
type SetupConfig struct {
	// Enabled turns the worktree setup step on for the whole project. Default
	// false (opt-in): when on, each freshly created slice worktree runs Command
	// after checkout and before any agent dispatch.
	Enabled bool `toml:"enabled"`
	// Command is the shell command run via `sh -c` in the slice worktree root
	// after the worktree is created and before agent dispatch (e.g.
	// "npm install"). Empty leaves setup inert even when Enabled is true. The
	// command environment includes SPRINGFIELD_SOURCE_ROOT (the main checkout)
	// and SPRINGFIELD_WORKTREE (the slice worktree).
	Command string `toml:"command"`
	// Teardown is an optional counterpart to Command run at slice cleanup, in
	// the same worktree root and with the same environment, immediately before
	// the execution worktree is removed. It exists to release resources that
	// live OUTSIDE the worktree — a database container, a docker-compose stack,
	// a bound port — that setup spun up and git-tracked removal alone leaves
	// running (e.g. "docker compose down"). It is strictly best-effort: a
	// failing teardown is logged and never blocks cleanup or changes the plan
	// outcome. Empty (the common case) skips teardown entirely. Governed by the
	// same Enabled toggle and Timeout as Command.
	Teardown string `toml:"teardown"`
	// Timeout is the wall-clock ceiling as a Go duration string (e.g. "20m",
	// "90s") applied to both Command and Teardown. Empty/unparseable →
	// DefaultSetupTimeout. Resolve via TimeoutOrDefault().
	Timeout string `toml:"timeout"`
}

// TimeoutOrDefault returns the configured setup timeout, or DefaultSetupTimeout
// when the key is unset or not a valid Go duration.
func (s SetupConfig) TimeoutOrDefault() time.Duration {
	str := strings.TrimSpace(s.Timeout)
	if str == "" {
		return DefaultSetupTimeout
	}
	d, err := time.ParseDuration(str)
	if err != nil || d <= 0 {
		return DefaultSetupTimeout
	}
	return d
}

// ShouldRun reports whether the setup step actually runs: the toggle must be on
// AND a non-empty command must be present. The command check is what preserves
// the opt-in guarantee — a project that flips Enabled on but configures no
// command stays inert rather than failing the slice with nothing to run.
func (s SetupConfig) ShouldRun() bool {
	return s.Enabled && strings.TrimSpace(s.Command) != ""
}

// ShouldTeardown reports whether the teardown step runs at slice cleanup: the
// [setup] toggle must be on AND a non-empty Teardown command must be present.
// Teardown is independent of Command — a project may configure teardown without
// a setup command (though the common case pairs them), so ShouldRun is not a
// precondition here.
func (s SetupConfig) ShouldTeardown() bool {
	return s.Enabled && strings.TrimSpace(s.Teardown) != ""
}
