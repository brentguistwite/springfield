package worktreesetup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Env var names exported into the setup command's environment. SourceRoot is
// the main checkout the batch was launched from; Worktree is the slice worktree
// the command runs in. A setup script uses them to copy untracked files (e.g.
// `cp "$SPRINGFIELD_SOURCE_ROOT/.env" "$SPRINGFIELD_WORKTREE/.env"`) that git
// never carried into the fresh checkout.
const (
	EnvSourceRoot = "SPRINGFIELD_SOURCE_ROOT"
	EnvWorktree   = "SPRINGFIELD_WORKTREE"
)

// Request configures a single worktree setup run.
type Request struct {
	// Command is the shell command executed via `sh -c` (e.g. "npm install").
	Command string
	// WorktreeRoot is the slice worktree the command runs in (its working
	// directory) and the value exported as SPRINGFIELD_WORKTREE.
	WorktreeRoot string
	// SourceRoot is the main checkout path exported as SPRINGFIELD_SOURCE_ROOT.
	SourceRoot string
	// Env carries additional environment variables merged over the base
	// environment (e.g. the slice's SPRINGFIELD_PORT/SPRINGFIELD_PORT_RANGE
	// block from portblock). SPRINGFIELD_SOURCE_ROOT and SPRINGFIELD_WORKTREE
	// are set unconditionally and win over any same-named key here.
	Env map[string]string
	// Timeout is the wall-clock ceiling. A run that exceeds it has its whole
	// process group killed and is reported with TimedOut=true. <=0 disables the
	// timeout.
	Timeout time.Duration
}

// Result is the observable outcome of one setup run.
type Result struct {
	// ExitCode is the command's exit status. -1 when the process was killed by
	// a signal (including a timeout kill) or never started.
	ExitCode int
	// Stdout and Stderr are the fully captured streams.
	Stdout string
	Stderr string
	// TimedOut is true when Timeout elapsed and the process group was killed.
	TimedOut bool
	// Duration is the wall-clock time the command ran.
	Duration time.Duration
	// Err is non-nil only when the command could not be launched (e.g. the
	// worktree directory does not exist). A non-zero ExitCode or a timeout is
	// NOT an Err — those are ordinary failed setups the caller acts on.
	Err error
}

// Run executes req.Command via `sh -c` in req.WorktreeRoot, exporting
// SPRINGFIELD_SOURCE_ROOT and SPRINGFIELD_WORKTREE into the environment and
// capturing stdout and stderr in full. When req.Timeout elapses (or ctx is
// cancelled) the command's entire process group is killed so that children the
// shell spawned do not outlive the setup as orphans. It always returns a
// Result; Err is set only for a launch failure.
func Run(ctx context.Context, req Request) Result {
	if ctx == nil {
		ctx = context.Background()
	}

	proc := exec.Command("sh", "-c", req.Command)
	proc.Dir = req.WorktreeRoot
	env := os.Environ()
	// Caller-supplied vars first so the two SPRINGFIELD_SOURCE_ROOT/WORKTREE
	// entries appended last win on a key collision (last-wins in exec env).
	for k, v := range req.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	proc.Env = append(env,
		fmt.Sprintf("%s=%s", EnvSourceRoot, req.SourceRoot),
		fmt.Sprintf("%s=%s", EnvWorktree, req.WorktreeRoot),
	)
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

	var timedOut bool
	select {
	case <-waitDone:
	case <-timeoutCh:
		timedOut = true
		killGroup(proc)
		<-waitDone
	case <-ctx.Done():
		killGroup(proc)
		<-waitDone
	}

	dur := time.Since(start)
	exitCode := -1
	if proc.ProcessState != nil {
		exitCode = proc.ProcessState.ExitCode()
	}
	return Result{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		TimedOut: timedOut,
		Duration: dur,
	}
}
