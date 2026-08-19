package notify_test

import (
	"testing"

	"springfield/internal/features/notify"
)

// TestKindsAreDistinct pins the four terminal-state kinds to distinct values so
// the seam can switch on Kind without two states colliding.
func TestKindsAreDistinct(t *testing.T) {
	kinds := []notify.Kind{
		notify.NeedsHuman,
		notify.Complete,
		notify.Failed,
		notify.CostCapped,
	}
	seen := map[notify.Kind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Fatalf("duplicate Kind value %d", k)
		}
		seen[k] = true
	}
}

// TestNopDeliversNothingAndNeverPanics locks the default Notifier's contract:
// it is a valid Notifier (used when notifications are unconfigured) and swallows
// every Event without side effects or panics.
func TestNopDeliversNothingAndNeverPanics(t *testing.T) {
	var n notify.Notifier = notify.Nop{}
	n.Notify(notify.Event{Kind: notify.Failed, BatchID: "b1", Detail: "boom"})
	n.Notify(notify.Event{Kind: notify.CostCapped, BatchID: "b1", SpendUSD: 12.5})
}
