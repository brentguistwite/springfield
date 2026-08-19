package verify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Request configures a single verify command run.
type Request struct {
	// Command is the shell command executed via `sh -c` (e.g. "go test ./...").
	Command string
	// Dir is the working directory the command runs in (the worktree root).
	// Empty means the current process's working directory.
	Dir string
	// Env carries additional environment variables merged over the base
	// environment (e.g. the slice's SPRINGFIELD_PORT/SPRINGFIELD_PORT_RANGE
	// block from portblock), so a verify suite binds the same ports the agent
	// and setup command saw. Empty leaves the process environment untouched.
	Env map[string]string
	// Timeout is the wall-clock ceiling. A run that exceeds it has its whole
	// process group killed and is reported with TimedOut=true. <=0 disables the
	// timeout.
	Timeout time.Duration
}

// Result is the observable outcome of one command run.
type Result struct {
	// ExitCode is the command's exit status. -1 when the process was killed by
	// a signal (including a timeout kill) or never started.
	ExitCode int
	// Stdout and Stderr are the fully captured streams.
	Stdout string
	Stderr string
	// TimedOut is true when Timeout elapsed and the process group was killed.
	TimedOut bool
	// Cancelled is true when the caller's context (e.g. a SIGINT-driven abort)
	// fired and the process group was killed as a result — NOT a timeout. It
	// distinguishes a user/caller abort from an ordinary non-zero exit so the
	// gate does not misclassify an abort as a fixable failed round.
	Cancelled bool
	// Duration is the wall-clock time the command ran.
	Duration time.Duration
	// Err is non-nil only when the command could not be launched (e.g. the
	// working directory does not exist). A non-zero ExitCode or a timeout is
	// NOT an Err — those are ordinary failed rounds the gate acts on.
	Err error
}

// Run executes req.Command via `sh -c` in req.Dir, capturing stdout and stderr
// in full. When req.Timeout elapses (or ctx is cancelled) the command's entire
// process group is killed so that children the shell spawned — the `go test`
// compiler and test binaries, for instance — do not outlive the round as
// orphans. It always returns a Result; Err is set only for a launch failure.
func Run(ctx context.Context, req Request) Result {
	if ctx == nil {
		ctx = context.Background()
	}

	proc := exec.Command("sh", "-c", req.Command)
	proc.Dir = req.Dir
	if len(req.Env) > 0 {
		env := os.Environ()
		for k, v := range req.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		proc.Env = env
	}
	var stdout, stderr bytes.Buffer
	proc.Stdout = &stdout
	proc.Stderr = &stderr
	setProcGroup(proc)

	start := time.Now()
	if err := proc.Start(); err != nil {
		return Result{ExitCode: -1, Err: err, Duration: time.Since(start)}
	}

	// Wait in a goroutine so the select below can race the process against the
	// timeout and context without blocking. proc.Wait also drains the stdout/
	// stderr copy goroutines, so the buffers are complete once it returns.
	waitDone := make(chan error, 1)
	go func() { waitDone <- proc.Wait() }()

	var timeoutCh <-chan time.Time
	if req.Timeout > 0 {
		t := time.NewTimer(req.Timeout)
		defer t.Stop()
		timeoutCh = t.C
	}

	var timedOut, cancelled bool
	select {
	case <-waitDone:
	case <-timeoutCh:
		timedOut = true
		killGroup(proc)
		<-waitDone
	case <-ctx.Done():
		cancelled = true
		killGroup(proc)
		<-waitDone
	}

	dur := time.Since(start)
	exitCode := -1
	if proc.ProcessState != nil {
		exitCode = proc.ProcessState.ExitCode()
	}
	return Result{
		ExitCode:  exitCode,
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		TimedOut:  timedOut,
		Cancelled: cancelled,
		Duration:  dur,
	}
}
