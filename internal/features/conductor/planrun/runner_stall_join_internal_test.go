package planrun

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"springfield/internal/features/conductor"
	"springfield/internal/features/notify"
)

// blockingNotifier holds the stall escalation callback in-flight: Notify (the
// last side effect stallHook performs) signals it has entered, then blocks until
// released. It lets a test pin the callback mid-flight so the join contract of
// newVerifyStall's stop function can be observed directly.
type blockingNotifier struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingNotifier) Notify(notify.Event) {
	b.entered <- struct{}{}
	<-b.release
}

// rawProjectFixture builds a minimal loaded Project rooted at a temp dir so
// stallHook's UpdatePlan/SaveState have a real runtime to write through. It uses
// LoadProjectRaw (no plan_units validation) since this test exercises only the
// escalation callback, not plan compilation.
func rawProjectFixture(t *testing.T) *conductor.Project {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"), 0o644); err != nil {
		t.Fatalf("toml: %v", err)
	}
	cfgPath := filepath.Join(root, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	project, err := conductor.LoadProjectRaw(root)
	if err != nil {
		t.Fatalf("LoadProjectRaw: %v", err)
	}
	return project
}

// TestNewVerifyStallJoinsWatcherBeforeStopReturns pins the join contract that
// keeps a verify-gate wedge escalation from outliving the gate: the stop function
// newVerifyStall returns MUST NOT return while an in-flight stallHook callback is
// still running. Without the join (a bare context cancel), stop returns
// immediately while the callback is mid-flight, so a late escalation can re-stamp
// PlanState.Stall onto an already-finished plan after the terminal write — the
// exact race the exec.Run watcher join closes on the story-loop path, which the
// verify-gate path must close too.
func TestNewVerifyStallJoinsWatcherBeforeStopReturns(t *testing.T) {
	bn := &blockingNotifier{entered: make(chan struct{}), release: make(chan struct{})}
	in := SinglePlanInput{Project: rawProjectFixture(t), Notifier: bn}

	// Tiny threshold + real clock so the watcher classifies the (silent) detector
	// possibly-wedged within milliseconds and fires the escalation callback.
	det, stop := in.newVerifyStall("p", t.TempDir(), 5*time.Millisecond, time.Now, nil)
	if det == nil {
		t.Fatal("positive threshold must build a detector")
	}

	// Wait for the escalation callback to enter (it now blocks in Notify).
	select {
	case <-bn.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("stall escalation callback never fired")
	}

	// Call stop while the callback is pinned in-flight. A watcher-joining stop
	// must block until the callback completes; a cancel-only stop returns early.
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()

	select {
	case <-stopped:
		t.Fatal("stop() returned while an in-flight stall callback was still running — the verify-gate watcher was not joined")
	case <-time.After(100 * time.Millisecond):
		// Expected: stop is blocked joining the watcher goroutine.
	}

	close(bn.release) // let the callback finish

	select {
	case <-stopped:
		// Expected: stop returns once the joined watcher goroutine exits.
	case <-time.After(2 * time.Second):
		t.Fatal("stop() did not return after the in-flight callback completed")
	}
}
