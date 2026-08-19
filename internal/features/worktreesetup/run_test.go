package worktreesetup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/worktreesetup"
)

func TestRun_ExitZeroCapturesStreams(t *testing.T) {
	res := worktreesetup.Run(context.Background(), worktreesetup.Request{
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

func TestRun_ExitNonZero(t *testing.T) {
	res := worktreesetup.Run(context.Background(), worktreesetup.Request{Command: "exit 3"})
	if res.Err != nil {
		t.Fatalf("unexpected launch error: %v", res.Err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", res.ExitCode)
	}
}

// TestRun_InjectsEnvVars proves the command environment carries
// SPRINGFIELD_SOURCE_ROOT and SPRINGFIELD_WORKTREE — the contract a setup
// script relies on to copy untracked files between the two checkouts.
func TestRun_InjectsEnvVars(t *testing.T) {
	wt := t.TempDir()
	src := t.TempDir()
	res := worktreesetup.Run(context.Background(), worktreesetup.Request{
		Command:      `echo "src=$SPRINGFIELD_SOURCE_ROOT wt=$SPRINGFIELD_WORKTREE"`,
		WorktreeRoot: wt,
		SourceRoot:   src,
	})
	if res.Err != nil {
		t.Fatalf("unexpected launch error: %v", res.Err)
	}
	out := strings.TrimSpace(res.Stdout)
	if !strings.Contains(out, "src="+src) {
		t.Errorf("stdout %q missing SPRINGFIELD_SOURCE_ROOT=%s", out, src)
	}
	if !strings.Contains(out, "wt="+wt) {
		t.Errorf("stdout %q missing SPRINGFIELD_WORKTREE=%s", out, wt)
	}
}

// TestRun_RunsInWorktreeRoot proves WorktreeRoot is the working directory: a
// file written by the command lands there. Reading a marker (not `pwd`)
// sidesteps macOS /var→/private/var symlink noise.
func TestRun_RunsInWorktreeRoot(t *testing.T) {
	wt := t.TempDir()
	res := worktreesetup.Run(context.Background(), worktreesetup.Request{
		Command:      "echo made-it > created.txt",
		WorktreeRoot: wt,
	})
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("run failed: err=%v exit=%d", res.Err, res.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(wt, "created.txt")); err != nil {
		t.Fatalf("command did not run in worktree root: %v", err)
	}
}

func TestRun_LaunchFailureSetsErr(t *testing.T) {
	res := worktreesetup.Run(context.Background(), worktreesetup.Request{
		Command:      "echo hi",
		WorktreeRoot: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if res.Err == nil {
		t.Fatalf("expected launch error for missing worktree dir, got nil")
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1 on launch failure", res.ExitCode)
	}
}

func TestRun_TimeoutKillsAndFlags(t *testing.T) {
	res := worktreesetup.Run(context.Background(), worktreesetup.Request{
		Command: "sleep 30",
		Timeout: 100 * time.Millisecond,
	})
	if !res.TimedOut {
		t.Fatalf("TimedOut = false, want true")
	}
	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero after timeout kill")
	}
}
