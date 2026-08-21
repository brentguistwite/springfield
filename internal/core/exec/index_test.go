package exec_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"springfield/internal/core/exec"
	"springfield/internal/core/stall"
)

// TestRunStallClassificationNeverKillsSubprocess drives a silent-but-alive fake
// process (a bare sleep that emits nothing) past a short stall threshold and
// asserts the process is never signalled or killed: it exits 0, returns no
// error, and ran to its own completion. Classification is advisory only.
func TestRunStallClassificationNeverKillsSubprocess(t *testing.T) {
	fired := int64(0)
	det := stall.New(30*time.Millisecond, nil, func() { atomic.AddInt64(&fired, 1) })

	start := time.Now()
	res := exec.Run(context.Background(), exec.Command{
		Name:  "sh",
		Args:  []string{"-c", "sleep 0.3"}, // silent for 10x the threshold
		Stall: det,
	}, nil)
	elapsed := time.Since(start)

	if res.ExitCode != 0 {
		t.Fatalf("silent process should exit 0 (not killed), got exit %d err %v", res.ExitCode, res.Err)
	}
	if res.Err != nil {
		t.Fatalf("silent process should complete without error, got %v", res.Err)
	}
	if elapsed < 300*time.Millisecond {
		t.Fatalf("process was cut short (%s < 300ms) — it must run to its own completion", elapsed)
	}
	if atomic.LoadInt64(&fired) == 0 {
		t.Fatal("stall watcher should have classified the silent process possibly-wedged")
	}
}

// TestRunEventsResetStallTimer proves that a process emitting output faster than
// the threshold keeps the idle timer reset, so it is never classified wedged.
func TestRunEventsResetStallTimer(t *testing.T) {
	fired := int64(0)
	det := stall.New(120*time.Millisecond, nil, func() { atomic.AddInt64(&fired, 1) })

	res := exec.Run(context.Background(), exec.Command{
		Name: "sh",
		// Emits a line every 20ms for ~200ms: each event resets the 120ms timer.
		Args:  []string{"-c", "for i in 1 2 3 4 5 6 7 8 9 10; do echo tick; sleep 0.02; done"},
		Stall: det,
	}, nil)

	if res.ExitCode != 0 {
		t.Fatalf("process should exit 0, got %d err %v", res.ExitCode, res.Err)
	}
	if got := atomic.LoadInt64(&fired); got != 0 {
		t.Fatalf("a steadily-emitting process must not be classified wedged; fired %d times", got)
	}
}
