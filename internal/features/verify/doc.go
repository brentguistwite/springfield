// Package verify runs a project's verify command (e.g. "go test ./...") as an
// objective completion gate: the command must exit 0 before a plan is honored
// complete. Run is the deep-module entry point — it executes the command via
// `sh -c` in a given directory, captures stdout and stderr in full, enforces a
// wall-clock timeout by killing the entire process group, and reports the exit
// code, captured output, whether it timed out, and how long it ran.
//
// A non-zero exit or a timeout is an ordinary failed round, not a Run error;
// Result.Err is reserved for a failure to launch the command at all (e.g. a
// missing working directory).
package verify
