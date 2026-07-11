package verify_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/verify"
)

func TestRun_ExitZero(t *testing.T) {
	res := verify.Run(context.Background(), verify.Request{Command: "echo hi"})
	if res.Err != nil {
		t.Fatalf("unexpected launch error: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if res.TimedOut {
		t.Fatalf("TimedOut = true, want false")
	}
	if got := strings.TrimSpace(res.Stdout); got != "hi" {
		t.Fatalf("stdout = %q, want %q", got, "hi")
	}
	if res.Duration <= 0 {
		t.Fatalf("Duration = %v, want > 0", res.Duration)
	}
}

func TestRun_ExitNonZero(t *testing.T) {
	res := verify.Run(context.Background(), verify.Request{Command: "exit 1"})
	if res.Err != nil {
		t.Fatalf("unexpected launch error: %v", res.Err)
	}
	if res.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", res.ExitCode)
	}
	if res.TimedOut {
		t.Fatalf("TimedOut = true, want false")
	}
}

func TestRun_CapturesStdoutAndStderr(t *testing.T) {
	// A command writing to both streams: each must land in its own field.
	res := verify.Run(context.Background(), verify.Request{
		Command: "echo to-out; echo to-err 1>&2",
	})
	if res.Err != nil {
		t.Fatalf("unexpected launch error: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if got := strings.TrimSpace(res.Stdout); got != "to-out" {
		t.Fatalf("stdout = %q, want %q", got, "to-out")
	}
	if got := strings.TrimSpace(res.Stderr); got != "to-err" {
		t.Fatalf("stderr = %q, want %q", got, "to-err")
	}
}

func TestRun_CwdHonored(t *testing.T) {
	// Prove Dir is used by reading a file that only exists inside it. Using a
	// file read (not `pwd`) sidesteps macOS /var→/private/var symlink noise.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("found-it"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := verify.Run(context.Background(), verify.Request{
		Command: "cat marker.txt",
		Dir:     dir,
	})
	if res.Err != nil {
		t.Fatalf("unexpected launch error: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", res.ExitCode, res.Stderr)
	}
	if got := strings.TrimSpace(res.Stdout); got != "found-it" {
		t.Fatalf("stdout = %q, want %q", got, "found-it")
	}
}

func TestRun_TimeoutKillsProcessGroup(t *testing.T) {
	// The shell backgrounds a grandchild that would create a sentinel file 1s
	// from now, then blocks for 3s. With a 200ms timeout the whole process
	// GROUP is killed: if only the shell were killed, the orphaned background
	// subshell would survive and create the sentinel. Asserting the sentinel
	// never appears is what proves group-kill, not merely single-process-kill.
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "sentinel")
	cmd := fmt.Sprintf(`(sleep 1 && touch %q) & sleep 3`, sentinel)

	start := time.Now()
	res := verify.Run(context.Background(), verify.Request{
		Command: cmd,
		Dir:     dir,
		Timeout: 200 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if !res.TimedOut {
		t.Fatalf("TimedOut = false, want true")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took %v; timeout should have killed it near 200ms", elapsed)
	}
	if res.Duration <= 0 {
		t.Fatalf("Duration = %v, want > 0", res.Duration)
	}

	// Wait past the grandchild's 1s delay; a surviving orphan would fire by now.
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("sentinel %s exists — background child outlived the timeout, process group was NOT killed", sentinel)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat sentinel: %v", err)
	}
}

func TestRun_ContextCancelledMidCommand(t *testing.T) {
	// A context cancelled WHILE the command runs must be reported distinctly from
	// an ordinary failed command: Cancelled=true, TimedOut=false, Err=nil (a
	// cancel is neither a timeout nor a launch failure). This is what lets the
	// gate tell a user abort apart from a fixable failed round.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	res := verify.Run(ctx, verify.Request{Command: "sleep 3"})
	elapsed := time.Since(start)

	if !res.Cancelled {
		t.Fatalf("Cancelled = false, want true for a mid-command context cancel")
	}
	if res.TimedOut {
		t.Fatalf("TimedOut = true, want false (this was a cancel, not a timeout)")
	}
	if res.Err != nil {
		t.Fatalf("Err = %v, want nil (a cancel is not a launch failure)", res.Err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Run took %v; cancel should have killed the command near 100ms", elapsed)
	}
}

func TestRun_LaunchErrorOnMissingDir(t *testing.T) {
	// A non-existent working directory can't be entered: surfaced as Err (a
	// launch failure), distinct from a command that ran and exited non-zero.
	res := verify.Run(context.Background(), verify.Request{
		Command: "echo hi",
		Dir:     filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if res.Err == nil {
		t.Fatalf("Err = nil, want a launch error for a missing Dir")
	}
	if res.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1 on launch failure", res.ExitCode)
	}
}
