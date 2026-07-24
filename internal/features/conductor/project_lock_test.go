package conductor_test

import (
	"fmt"
	"sync"
	"testing"

	"springfield/internal/features/conductor"
)

// TestProjectConcurrentPlanAccessIsRaceFree exercises the locked Project API
// the way parallel plan execution does: one goroutine per plan mutating its
// own entry and saving, while another goroutine reads across all plans. Run
// under -race this locks in the map-safety + marshal-consistency contract.
func TestProjectConcurrentPlanAccessIsRaceFree(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	const workers = 8
	const iters = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		planID := fmt.Sprintf("plan-%d", w)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				project.UpdatePlan(planID, func(ps *conductor.PlanState) {
					ps.Status = conductor.StatusRunning
					ps.Attempts++
				})
				if err := project.SaveState(); err != nil {
					t.Errorf("SaveState: %v", err)
					return
				}
			}
		}()
	}
	// Concurrent readers across every plan, including entries mid-creation.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < workers*iters; i++ {
			for w := 0; w < workers; w++ {
				planID := fmt.Sprintf("plan-%d", w)
				_ = project.PlanStatus(planID)
				_, _ = project.ReadPlan(planID)
			}
			_ = project.PlansSnapshot()
		}
	}()
	wg.Wait()

	for w := 0; w < workers; w++ {
		planID := fmt.Sprintf("plan-%d", w)
		ps, ok := project.ReadPlan(planID)
		if !ok || ps.Attempts != iters {
			t.Errorf("plan %s: attempts = %d (ok=%v), want %d", planID, ps.Attempts, ok, iters)
		}
	}
}

// TestUpdatePlanCreatesEntryAndReadPlanReturnsCopy verifies the two contract
// details callers rely on: UpdatePlan creates missing entries, and ReadPlan
// returns a detached copy (mutating it must not touch project state).
func TestUpdatePlanCreatesEntryAndReadPlanReturnsCopy(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	project.UpdatePlan("fresh", func(ps *conductor.PlanState) {
		ps.Status = conductor.StatusCompleted
	})
	got, ok := project.ReadPlan("fresh")
	if !ok || got.Status != conductor.StatusCompleted {
		t.Fatalf("ReadPlan(fresh) = %+v, %v; want completed entry", got, ok)
	}

	got.Status = conductor.StatusFailed
	again, _ := project.ReadPlan("fresh")
	if again.Status != conductor.StatusCompleted {
		t.Fatalf("ReadPlan must return a copy; state mutated to %q", again.Status)
	}

	if _, ok := project.ReadPlan("missing"); ok {
		t.Fatalf("ReadPlan(missing) reported ok")
	}
}

// TestPlansSnapshotIsDetached verifies the snapshot is a deep copy at the
// PlanState level: mutating snapshot entries must not touch live state.
func TestPlansSnapshotIsDetached(t *testing.T) {
	root := newProjectRoot(t)
	plansDir := writeProjectAndPlan(t, root, "feature.md")
	project := loadProjectWithDefaults(t, root, plansDir)

	project.UpdatePlan("a", func(ps *conductor.PlanState) { ps.WorktreePath = "/wt/a" })
	snap := project.PlansSnapshot()
	if snap["a"] == nil || snap["a"].WorktreePath != "/wt/a" {
		t.Fatalf("snapshot missing plan a: %+v", snap["a"])
	}
	snap["a"].WorktreePath = "/mutated"
	live, _ := project.ReadPlan("a")
	if live.WorktreePath != "/wt/a" {
		t.Fatalf("snapshot mutation leaked into live state: %q", live.WorktreePath)
	}
}
