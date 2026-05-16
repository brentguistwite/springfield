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

// TestRunHasCostCappedField is a trip-wire: --cost-cap relies on this typed
// state to distinguish a pause-and-resume from a fatal failure. A future
// refactor that silently drops it (e.g. trying to fold "cost cap exceeded"
// into FatalError as a string prefix) regresses to the rejected design
// from the 2026-05-15 vendor-economics-pivot plan, R1 review.
func TestRunHasCostCappedField(t *testing.T) {
	ty := reflect.TypeOf(batch.Run{})
	field, ok := ty.FieldByName("CostCapped")
	if !ok {
		t.Fatal("CostCapped field missing on batch.Run; required by --cost-cap (see plans/2026-05-15-vendor-economics-pivot)")
	}
	if field.Type.Kind() != reflect.Bool {
		t.Fatalf("CostCapped should be bool, got %v", field.Type.Kind())
	}
	tag := field.Tag.Get("json")
	if tag != "cost_capped,omitempty" {
		t.Fatalf("CostCapped JSON tag drift: got %q want %q", tag, "cost_capped,omitempty")
	}
}
