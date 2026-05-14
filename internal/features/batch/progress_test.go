package batch_test

import (
	"reflect"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
)

func integrated() *conductor.PlanState {
	return &conductor.PlanState{Status: conductor.StatusCompleted}
}

func running() *conductor.PlanState {
	return &conductor.PlanState{Status: conductor.StatusRunning}
}

func failed() *conductor.PlanState {
	return &conductor.PlanState{Status: conductor.StatusFailed}
}

func makeBatch(planIDs []string, phases ...batch.Phase) batch.Batch {
	return batch.Batch{ID: "b", Title: "t", PlanIDs: planIDs, Phases: phases}
}

func TestComputeProgress_EmptyBatch(t *testing.T) {
	got := batch.ComputeProgress(batch.Batch{}, nil)
	if got.TotalPlans != 0 {
		t.Errorf("TotalPlans: got %d, want 0", got.TotalPlans)
	}
	if got.AllDone {
		t.Error("AllDone should be false for empty batch")
	}
	if got.CurrentPhaseIdx != -1 {
		t.Errorf("CurrentPhaseIdx: got %d, want -1", got.CurrentPhaseIdx)
	}
}

func TestComputeProgress_NilStateAllPending(t *testing.T) {
	b := makeBatch([]string{"a", "b"}, batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"a", "b"}})
	got := batch.ComputeProgress(b, nil)
	if !reflect.DeepEqual(got.Pending, []string{"a", "b"}) {
		t.Errorf("Pending: got %v, want [a b]", got.Pending)
	}
	if len(got.Done) != 0 || len(got.InFlight) != 0 {
		t.Errorf("Done/InFlight should be empty, got Done=%v InFlight=%v", got.Done, got.InFlight)
	}
	if got.CurrentPhaseIdx != 0 {
		t.Errorf("CurrentPhaseIdx: got %d, want 0", got.CurrentPhaseIdx)
	}
}

func TestComputeProgress_OneIntegratedSerial(t *testing.T) {
	b := makeBatch([]string{"a", "b", "c"}, batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"a", "b", "c"}})
	state := &conductor.State{Plans: map[string]*conductor.PlanState{"a": integrated()}}
	got := batch.ComputeProgress(b, state)
	if got.DonePlans != 1 {
		t.Errorf("DonePlans: got %d, want 1", got.DonePlans)
	}
	if !reflect.DeepEqual(got.Done, []string{"a"}) {
		t.Errorf("Done: got %v, want [a]", got.Done)
	}
	if got.CurrentPhaseIdx != 0 {
		t.Errorf("CurrentPhaseIdx: got %d, want 0", got.CurrentPhaseIdx)
	}
}

func TestComputeProgress_OneRunningMidPhase(t *testing.T) {
	b := makeBatch([]string{"a", "b"}, batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"a", "b"}})
	state := &conductor.State{Plans: map[string]*conductor.PlanState{"a": running()}}
	got := batch.ComputeProgress(b, state)
	if !reflect.DeepEqual(got.InFlight, []string{"a"}) {
		t.Errorf("InFlight: got %v, want [a]", got.InFlight)
	}
	if got.ParallelInFlight {
		t.Error("ParallelInFlight should be false for one-in-flight serial phase")
	}
}

func TestComputeProgress_ParallelInFlight(t *testing.T) {
	b := makeBatch([]string{"a", "b"}, batch.Phase{Mode: batch.PhaseParallel, Plans: []string{"a", "b"}})
	state := &conductor.State{Plans: map[string]*conductor.PlanState{"a": running(), "b": running()}}
	got := batch.ComputeProgress(b, state)
	if !got.ParallelInFlight {
		t.Error("ParallelInFlight should be true: 2 in flight in parallel phase")
	}
	if len(got.InFlight) != 2 {
		t.Errorf("InFlight: got %d, want 2", len(got.InFlight))
	}
}

func TestComputeProgress_Phase1InFlight(t *testing.T) {
	b := makeBatch(
		[]string{"a", "b", "c"},
		batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"a"}},
		batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"b", "c"}},
	)
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		"a": integrated(),
		"b": running(),
	}}
	got := batch.ComputeProgress(b, state)
	if got.CurrentPhaseIdx != 1 {
		t.Errorf("CurrentPhaseIdx: got %d, want 1", got.CurrentPhaseIdx)
	}
}

func TestComputeProgress_AllDone(t *testing.T) {
	b := makeBatch([]string{"a", "b"}, batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"a", "b"}})
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		"a": integrated(),
		"b": integrated(),
	}}
	got := batch.ComputeProgress(b, state)
	if !got.AllDone {
		t.Error("AllDone should be true when every plan is integrated")
	}
	if got.CurrentPhaseIdx != -1 {
		t.Errorf("CurrentPhaseIdx: got %d, want -1 when all done", got.CurrentPhaseIdx)
	}
	if got.DonePlans != 2 {
		t.Errorf("DonePlans: got %d, want 2", got.DonePlans)
	}
}

func TestComputeProgress_MissingStateEntryIsPending(t *testing.T) {
	b := makeBatch([]string{"a", "b"}, batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"a", "b"}})
	state := &conductor.State{Plans: map[string]*conductor.PlanState{"a": integrated()}}
	got := batch.ComputeProgress(b, state)
	if !reflect.DeepEqual(got.Pending, []string{"b"}) {
		t.Errorf("Pending: got %v, want [b]", got.Pending)
	}
}

func TestComputeProgress_FatalStallStillSane(t *testing.T) {
	// One integrated, one failed, none in flight. ComputeProgress itself does
	// not read FatalError; this test confirms the rollup is well-defined when
	// a batch is stalled — Done counts the integrated plan, the failed plan
	// lands in Pending, CurrentPhaseIdx points at the stalled phase, AllDone
	// stays false.
	b := makeBatch(
		[]string{"a", "b"},
		batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"a"}},
		batch.Phase{Mode: batch.PhaseSerial, Plans: []string{"b"}},
	)
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		"a": integrated(),
		"b": failed(),
	}}
	got := batch.ComputeProgress(b, state)
	if got.DonePlans != 1 {
		t.Errorf("DonePlans: got %d, want 1", got.DonePlans)
	}
	if !reflect.DeepEqual(got.Pending, []string{"b"}) {
		t.Errorf("Pending: got %v, want [b]", got.Pending)
	}
	if got.CurrentPhaseIdx != 1 {
		t.Errorf("CurrentPhaseIdx: got %d, want 1", got.CurrentPhaseIdx)
	}
	if got.AllDone {
		t.Error("AllDone should be false when one plan is failed")
	}
}
