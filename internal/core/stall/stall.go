// Package stall implements event-recency stall detection: it classifies a
// silent, event-less slice as possibly-wedged once no event has arrived within
// a configurable threshold, sitting between the per-iteration turn cap and the
// subprocess wall-clock timeout.
//
// A Detector is fed a liveness heartbeat on every stream event (Observe) at the
// live consumption point — exec.Run's emit closure — and watches for staleness
// in the background (Watch). A stall verdict is ADVISORY ONLY: the Detector
// never signals, interrupts, or kills the subprocess. The caller decides how to
// escalate; the process runs to its own completion or the wall-clock deadline.
//
// Detection is suppressed while a verify command is actively running (Suppress),
// since a churning test suite is a legitimately busy, event-quiet phase, and is
// disabled entirely when the threshold is zero.
package stall

import (
	"context"
	"sync"
	"time"
)

// Detector tracks per-slice event recency and flags staleness past a threshold.
// It is safe for concurrent use: Observe/Suppress/Stale and the Watch goroutine
// all guard shared state under a single mutex.
type Detector struct {
	threshold time.Duration
	now       func() time.Time
	onStale   func()
	poll      time.Duration

	mu         sync.Mutex
	last       time.Time
	suppressed bool
	fired      bool
}

// New builds a Detector.
//
//   - threshold is the idle ceiling; a slice that emits no event within it is
//     classified possibly-wedged. threshold <= 0 disables detection entirely
//     (Stale always false, Watch never fires).
//   - now is the clock; nil defaults to time.Now. Tests inject a fake clock.
//   - onStale fires at most once per idle stretch when Watch observes staleness;
//     nil is a no-op. It MUST NOT touch the subprocess.
func New(threshold time.Duration, now func() time.Time, onStale func()) *Detector {
	if now == nil {
		now = time.Now
	}
	if onStale == nil {
		onStale = func() {}
	}
	d := &Detector{
		threshold: threshold,
		now:       now,
		onStale:   onStale,
		poll:      pollInterval(threshold),
	}
	d.last = now()
	return d
}

// pollInterval derives how often Watch samples the clock. A fraction of the
// threshold keeps detection prompt without busy-spinning, clamped so a tiny
// test threshold still polls fast and a large production threshold does not.
func pollInterval(threshold time.Duration) time.Duration {
	if threshold <= 0 {
		return time.Second
	}
	p := threshold / 10
	if p < time.Millisecond {
		p = time.Millisecond
	}
	if p > 15*time.Second {
		p = 15 * time.Second
	}
	return p
}

// Observe records a liveness heartbeat: an event arrived, so reset the idle
// timer and re-arm firing for the next idle stretch.
func (d *Detector) Observe() {
	d.mu.Lock()
	d.last = d.now()
	d.fired = false
	d.mu.Unlock()
}

// Suppress pauses (true) or resumes (false) wedge classification. The verify
// gate suppresses while its command runs — a legitimately busy but event-quiet
// phase. Resuming resets the idle timer so a long suppressed stretch does not
// instantly flag on resume.
func (d *Detector) Suppress(v bool) {
	d.mu.Lock()
	d.suppressed = v
	if !v {
		d.last = d.now()
		d.fired = false
	}
	d.mu.Unlock()
}

// Stale reports whether the slice is currently possibly-wedged: idle beyond the
// threshold and not suppressed. It is a pure read (no edge-trigger bookkeeping),
// intended for assertions and callers polling the current verdict. Detection
// disabled (threshold <= 0) is never stale.
func (d *Detector) Stale() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.threshold <= 0 || d.suppressed {
		return false
	}
	return d.now().Sub(d.last) >= d.threshold
}

// Watch runs the staleness watcher until ctx is cancelled, calling onStale at
// most once per idle stretch when staleness is first observed. exec.Run launches
// it in a goroutine for the subprocess's lifetime and cancels it on exit. Watch
// never touches the subprocess. A disabled detector (threshold <= 0) returns
// immediately.
func (d *Detector) Watch(ctx context.Context) {
	if d.threshold <= 0 {
		return
	}
	ticker := time.NewTicker(d.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if d.edgeStale() {
				d.onStale()
			}
		}
	}
}

// edgeStale is the once-per-idle-stretch edge trigger backing Watch: it returns
// true only on the first poll that observes staleness, latching fired until the
// next Observe (or Suppress-resume) re-arms it.
func (d *Detector) edgeStale() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.threshold <= 0 || d.suppressed || d.fired {
		return false
	}
	if d.now().Sub(d.last) >= d.threshold {
		d.fired = true
		return true
	}
	return false
}
