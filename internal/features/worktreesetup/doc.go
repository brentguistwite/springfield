// Package worktreesetup runs a project's [setup] command in a freshly created
// slice worktree, after checkout and before any agent is dispatched. It is the
// hook where a plan installs dependencies, copies untracked files like .env, or
// generates artifacts the agent would otherwise burn turns producing (or fail
// without). Modeled on Conductor's per-worktree setup script.
//
// Run is the deep-module entry point — it executes the command via `sh -c` in
// the worktree root with SPRINGFIELD_SOURCE_ROOT (the main checkout) and
// SPRINGFIELD_WORKTREE (the slice worktree) exported into its environment,
// captures stdout and stderr in full, enforces a wall-clock timeout by killing
// the entire process group, and reports the exit code, captured output, whether
// it timed out, and how long it ran. A non-zero exit is an ordinary failed
// setup, not a Run error; Result.Err is reserved for a failure to launch the
// command at all (e.g. a missing worktree directory).
//
// WriteEvidence persists the outcome under <evidenceDir>/setup/: setup.json
// (command, cwd, exit_code, duration, timed_out) plus tail-truncated stdout.txt
// and stderr.txt. It takes the whole Result so the exit code is recorded from
// source and cannot be dropped.
//
// MarkComplete/IsComplete/ClearComplete manage a completion marker under the
// same directory — the durable proof that a setup run EXITED ZERO. The marker is
// keyed on the command that earned it, so the runner gates a reuse resume on
// both worktree existence AND command identity: a worktree whose setup crashed
// midway, OR whose [setup] command changed since it last succeeded, re-runs
// setup instead of dispatching an agent into a half-built (or stale) tree.
package worktreesetup
