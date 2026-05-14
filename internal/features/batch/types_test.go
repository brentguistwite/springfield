package batch_test

import (
	"reflect"
	"testing"

	"springfield/internal/features/batch"
)

// TestRunHasNoActivePhaseIdx is a trip-wire: if a future maintainer resurrects
// Run.ActivePhaseIdx, CI fails immediately. The field was deleted because it
// had no writer in production code and the displayed "Phase: X of N" value
// lied about progress. The canonical source of truth is now
// batch.ComputeProgress.
// See plans/2026-05-12-batch-progress-rollup and the 2026-05-14 omnibus PR.
func TestRunHasNoActivePhaseIdx(t *testing.T) {
	ty := reflect.TypeOf(batch.Run{})
	if _, ok := ty.FieldByName("ActivePhaseIdx"); ok {
		t.Fatal("ActivePhaseIdx resurrected; replace with batch.ComputeProgress (see plans/2026-05-12-batch-progress-rollup)")
	}
}
