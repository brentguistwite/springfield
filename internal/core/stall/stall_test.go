package stall_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"springfield/internal/core/stall"
)

// fakeClock is a manually-advanced clock so staleness can be asserted without
// sleeping on the wall clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestStaleFlagsAfterThreshold pins the core idle-timer logic with a fake
// clock: past the threshold with no observed event, the detector reports the
// slice possibly-wedged.
func TestStaleFlagsAfterThreshold(t *testing.T) {
	clock := newFakeClock()
	d := stall.New(100*time.Millisecond, clock.Now, nil)

	if d.Stale() {
		t.Fatal("fresh detector should not be stale")
	}
	clock.Advance(99 * time.Millisecond)
	if d.Stale() {
		t.Fatal("just under threshold should not be stale")
	}
	clock.Advance(1 * time.Millisecond)
	if !d.Stale() {
		t.Fatal("at threshold should be stale")
	}
}

// TestObserveResetsTimer proves receipt of any event resets the idle timer.
func TestObserveResetsTimer(t *testing.T) {
	clock := newFakeClock()
	d := stall.New(100*time.Millisecond, clock.Now, nil)

	clock.Advance(99 * time.Millisecond)
	d.Observe() // event arrived just before the threshold
	clock.Advance(99 * time.Millisecond)
	if d.Stale() {
		t.Fatal("observe should have reset the idle timer; not stale yet")
	}
	clock.Advance(1 * time.Millisecond)
	if !d.Stale() {
		t.Fatal("threshold since last observe elapsed; should be stale")
	}
}

// TestSuppressSuppressesClassification proves an actively-running verify command
// (modeled by Suppress(true)) suppresses wedge classification even well past the
// threshold, and that resuming re-arms detection without instantly firing on the
// idle time accrued while suppressed.
func TestSuppressSuppressesClassification(t *testing.T) {
	clock := newFakeClock()
	d := stall.New(100*time.Millisecond, clock.Now, nil)

	d.Suppress(true)
	clock.Advance(10 * time.Second) // a long, event-quiet verify run
	if d.Stale() {
		t.Fatal("classification must be suppressed while verify runs")
	}

	d.Suppress(false)
	if d.Stale() {
		t.Fatal("resuming must reset the idle clock, not fire on the suppressed backlog")
	}
	clock.Advance(100 * time.Millisecond)
	if !d.Stale() {
		t.Fatal("after resume, a fresh idle stretch past threshold should be stale")
	}
}

// TestZeroThresholdDisables proves a zero threshold disables detection entirely.
func TestZeroThresholdDisables(t *testing.T) {
	clock := newFakeClock()
	fired := int64(0)
	d := stall.New(0, clock.Now, func() { atomic.AddInt64(&fired, 1) })

	clock.Advance(24 * time.Hour)
	if d.Stale() {
		t.Fatal("zero threshold must never report stale")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Watch(ctx)
	time.Sleep(20 * time.Millisecond)
	if atomic.LoadInt64(&fired) != 0 {
		t.Fatal("zero-threshold watcher must never fire onStale")
	}
}

// TestWatchFiresOnceThenReArms drives the real watcher goroutine with a short
// threshold (no real agent) and asserts onStale fires exactly once per idle
// stretch, and that Observe re-arms it for a subsequent stretch.
func TestWatchFiresOnceThenReArms(t *testing.T) {
	fired := make(chan struct{}, 8)
	d := stall.New(40*time.Millisecond, nil, func() { fired <- struct{}{} })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Watch(ctx)

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher should have fired onStale after the threshold")
	}

	// A single idle stretch must fire onStale exactly once, not repeatedly.
	select {
	case <-fired:
		t.Fatal("onStale fired twice for one idle stretch")
	case <-time.After(200 * time.Millisecond):
	}

	// An event re-arms detection: after Observe, a new idle stretch fires again.
	d.Observe()
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher should re-arm and fire again after Observe")
	}
}
