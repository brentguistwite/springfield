package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/lock"
	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
	"springfield/internal/features/statusview"
)

func TestStatusNoConfigPointsAtRegistrationFlow(t *testing.T) {
	root := newStatusRoot(t)

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "springfield init") {
		t.Fatalf("expected init hint, got:\n%s", out)
	}
	if strings.Contains(out, "springfield plan\"") {
		t.Fatalf("stale \"springfield plan\" hint leaked:\n%s", out)
	}
	// The no-config path points at init, NOT the plan skill — the skill hint is
	// reserved for the loadable-but-empty-registry path. Pin the distinction so
	// the two messages can't be collapsed into one.
	if strings.Contains(out, "/springfield:plan") {
		t.Fatalf("no-config hint should point at init, not the plan skill:\n%s", out)
	}
}

func TestStatusEmptyPlanRegistryPointsAtPlanSkill(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusConfig(t, root, []map[string]any{})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	// Empty-but-valid registry signposts the plan skill + start, matching
	// init's own Next: copy rather than the lower-level "plans add" verb.
	if !strings.Contains(out, "/springfield:plan") {
		t.Fatalf("expected plan skill hint, got:\n%s", out)
	}
	if !strings.Contains(out, "springfield start") {
		t.Fatalf("expected springfield start hint, got:\n%s", out)
	}
}

func TestStatusPlanRegistryWhenNoBatch(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "title": "Feature A", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "feature-b", "title": "Feature B", "path": ".springfield/plans/feature.md", "order": 2},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Plan registry:") {
		t.Fatalf("expected plan registry header:\n%s", out)
	}
	if !strings.Contains(out, "feature-a") || !strings.Contains(out, "feature-b") {
		t.Fatalf("missing plan ids:\n%s", out)
	}
	if !strings.Contains(out, "springfield start") {
		t.Fatalf("plan-registry status should advertise springfield start in parity 2:\n%s", out)
	}
	if !strings.Contains(out, "worktree") {
		t.Fatalf("expected worktree-based execution mention:\n%s", out)
	}
}

func TestStatusActiveBatchWinsArbitration(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Batch: batch-001") {
		t.Fatalf("expected batch header:\n%s", out)
	}
	if strings.Contains(out, "Plan registry:") {
		t.Fatalf("plan registry leaked into active-batch output:\n%s", out)
	}
}

func TestStatusRollupNothingRunning(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Plans: 0/1 integrated") {
		t.Fatalf("expected 0/1 rollup:\n%s", out)
	}
	if !strings.Contains(out, "Next: 01") {
		t.Fatalf("expected Next pointer:\n%s", out)
	}
	if strings.Contains(out, "Phase:") {
		t.Fatalf("stale Phase: line leaked:\n%s", out)
	}
}

func TestStatusRollupOneInFlight(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "02", "path": ".springfield/plans/feature.md", "order": 2},
	})
	writeActiveBatchN(t, root, "batch-001", "Active Batch", []string{"01", "02"})
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"01": map[string]any{"status": "running"},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Plans: 0/2 integrated") {
		t.Fatalf("expected Plans: 0/2 integrated:\n%s", out)
	}
	// No live springfield process holds the lock in this in-process test, so the
	// running-persisted plan is surfaced as stalled (parity with JSON's stalled).
	if !strings.Contains(out, "Stalled: 01 (no running springfield process") {
		t.Fatalf("expected Stalled line:\n%s", out)
	}
	// The batch is blocked (stalled, nothing running): "Next:" is suppressed so
	// the text does not imply 02 is about to run when nothing advances until the
	// operator recovers the stalled plan.
	if strings.Contains(out, "Next:") {
		t.Fatalf("Next: must be suppressed while the batch is blocked by a stalled plan:\n%s", out)
	}
}

func TestStatusRollupParallelInFlight(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "02", "path": ".springfield/plans/feature.md", "order": 2},
	})
	writeActiveBatchParallel(t, root, "batch-001", "Active Batch", []string{"01", "02"})
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"01": map[string]any{"status": "running"},
			"02": map[string]any{"status": "running"},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	// No live process in this in-process test → the parallel in-flight plans
	// surface as stalled. The live "Current: ... (parallel)" rendering is
	// covered directly by TestPrintProgressBlock_LiveVsStalled.
	if !strings.Contains(out, "Stalled: 01, 02 (no running springfield process") {
		t.Fatalf("expected Stalled parallel line:\n%s", out)
	}
}

// TestPrintProgressBlock_LiveVsStalled exercises the in-flight rendering of
// printProgressBlock directly (no lock/process dependency): live=true renders
// the running/parallel "Current:" line; live=false renders the "Stalled:" line.
func TestPrintProgressBlock_LiveVsStalled(t *testing.T) {
	serial := batch.Batch{
		ID:      "b",
		Title:   "T",
		PlanIDs: []string{"01", "02"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"01", "02"}}},
	}
	serialState := &conductor.State{Plans: map[string]*conductor.PlanState{
		"01": {Status: conductor.StatusRunning},
	}}

	t.Run("live_serial_running", func(t *testing.T) {
		var buf bytes.Buffer
		printProgressBlock(&buf, serial, serialState, true, nil)
		if !strings.Contains(buf.String(), "Current: 01 (running)") {
			t.Fatalf("live serial: want Current: 01 (running), got:\n%s", buf.String())
		}
	})

	t.Run("dead_serial_stalled", func(t *testing.T) {
		var buf bytes.Buffer
		printProgressBlock(&buf, serial, serialState, false, nil)
		if !strings.Contains(buf.String(), "Stalled: 01 (no running springfield process") {
			t.Fatalf("dead serial: want Stalled line, got:\n%s", buf.String())
		}
	})

	parallel := batch.Batch{
		ID:      "b",
		Title:   "T",
		PlanIDs: []string{"01", "02"},
		Phases:  []batch.Phase{{Mode: batch.PhaseParallel, Plans: []string{"01", "02"}}},
	}
	parallelState := &conductor.State{Plans: map[string]*conductor.PlanState{
		"01": {Status: conductor.StatusRunning},
		"02": {Status: conductor.StatusRunning},
	}}

	t.Run("live_parallel", func(t *testing.T) {
		var buf bytes.Buffer
		printProgressBlock(&buf, parallel, parallelState, true, nil)
		if !strings.Contains(buf.String(), "Current: 01, 02 (parallel)") {
			t.Fatalf("live parallel: want Current: 01, 02 (parallel), got:\n%s", buf.String())
		}
	})

	// The "parallel" signal must key off the SAME running/stalled classifier
	// (ComposeStatus) the per-plan status uses — not ClassifyPlan, which counts
	// only StatusRunning and so would render this two-running parallel phase as
	// "(running)". An interrupted plan with a live process owning the lock is
	// running (it's being resumed), so two such plans are running in parallel.
	mixedState := &conductor.State{Plans: map[string]*conductor.PlanState{
		"01": {Status: conductor.StatusRunning},
		"02": {Status: conductor.StatusInterrupted},
	}}
	t.Run("live_parallel_running_plus_interrupted", func(t *testing.T) {
		var buf bytes.Buffer
		printProgressBlock(&buf, parallel, mixedState, true, nil)
		if !strings.Contains(buf.String(), "Current: 01, 02 (parallel)") {
			t.Fatalf("live mixed parallel: want Current: 01, 02 (parallel), got:\n%s", buf.String())
		}
	})
}

// TestPrintProgressBlock_RendersActivityThroughSharedProjection is the text↔JSON
// activity-parity guard (US-008): the "Current:" block renders each running
// plan's in-flight activity, and the rendered content comes from the SAME
// statusview.DeriveActivity projection the JSON view-model uses — so the two
// surfaces can never disagree about what a running plan is doing. It also pins
// the truthful-silence path: a running plan with no derivable activity prints no
// line rather than an invented phase.
func TestPrintProgressBlock_RendersActivityThroughSharedProjection(t *testing.T) {
	b := batch.Batch{
		ID: "b", Title: "T", PlanIDs: []string{"01", "02"},
		Phases: []batch.Phase{{Mode: batch.PhaseParallel, Plans: []string{"01", "02"}}},
	}
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		// A written fine-counter stamp: reviewing, round 2.
		"01": {Status: conductor.StatusRunning, Activity: &conductor.PlanActivity{
			Phase: "reviewing", Detail: "US-007", Round: 2, UpdatedAt: time.Now(),
		}},
		// Running but unstamped and no PRD → no derivable activity → no line.
		"02": {Status: conductor.StatusRunning},
	}}

	var buf bytes.Buffer
	printProgressBlock(&buf, b, state, true, nil) // live → both running
	out := buf.String()

	if !strings.Contains(out, "Current: 01, 02 (parallel)") {
		t.Fatalf("precondition: want both plans in the Current line, got:\n%s", out)
	}
	// Plan 01's stamped activity renders, formatted from the shared projection.
	want := "  01: " + formatActivity(statusview.DeriveActivity(state.Plans["01"], true, nil))
	if !strings.Contains(out, want) {
		t.Fatalf("want activity line %q for plan 01, got:\n%s", want, out)
	}
	if !strings.Contains(out, "reviewing US-007 (round 2)") {
		t.Fatalf("activity line must carry phase/detail/round, got:\n%s", out)
	}
	// Plan 02 has no derivable activity → truthful silence, no "02:" activity line.
	if strings.Contains(out, "  02:") {
		t.Fatalf("unstamped running plan must print no activity line, got:\n%s", out)
	}
}

// TestPrintProgressBlock_ActivityDerivesCurrentStoryFromPRD pins that the text
// activity line uses Tier-1 derivation from durable prd.json truth — with ZERO
// written stamp, the current story surfaces via the same projection JSON uses.
func TestPrintProgressBlock_ActivityDerivesCurrentStoryFromPRD(t *testing.T) {
	b := batch.Batch{
		ID: "b", Title: "T", PlanIDs: []string{"01"},
		Phases: []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"01"}}},
	}
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		"01": {Status: conductor.StatusRunning},
	}}
	prds := map[string]prd.PRD{"01": {UserStories: []prd.UserStory{
		{ID: "US-001", Priority: 1, Passes: true},
		{ID: "US-002", Priority: 2, Passes: false},
	}}}

	var buf bytes.Buffer
	printProgressBlock(&buf, b, state, true, prds)
	out := buf.String()

	if !strings.Contains(out, "  01: implementing US-002") {
		t.Fatalf("want derived current-story activity line, got:\n%s", out)
	}
	// Derivation must not mutate persisted state.
	if state.Plans["01"].Activity != nil {
		t.Fatalf("text derivation wrote to PlanState.Activity: %+v", state.Plans["01"].Activity)
	}
}

// TestPrintProgressBlock_SurfacesEveryStatus locks in text↔JSON parity at the
// classification level: every status ComposeStatus can emit (and that the JSON
// view-model surfaces per-plan) has a home in the text progress block. Before
// this, failed/needs-human/done plans fell through the running/stalled/pending
// switch and were silently omitted — a human reading text could not see what
// JSON makes explicit (e.g. which plan in a multi-plan batch actually failed).
func TestPrintProgressBlock_SurfacesEveryStatus(t *testing.T) {
	b := batch.Batch{
		ID:      "b",
		Title:   "T",
		PlanIDs: []string{"01", "02", "03", "04"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"01", "02", "03", "04"}}},
	}
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		"01": {Status: conductor.StatusFailed, Error: "boom"},
		"02": {Status: conductor.StatusNeedsHuman},
		// completed but not integrated (merge refused) → "done"
		"03": {Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeRefused}},
		"04": {Status: conductor.StatusPending},
	}}

	var buf bytes.Buffer
	printProgressBlock(&buf, b, state, false, nil)
	out := buf.String()

	for _, want := range []string{
		"Failed: 01",
		"Needs human: 02",
		"Done (not integrated): 03",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in progress block; got:\n%s", want, out)
		}
	}
	// The batch is blocked (failed/needs-human present, nothing running), so the
	// "Next:" hint is suppressed — nothing advances until the operator acts.
	if strings.Contains(out, "Next:") {
		t.Errorf("Next: must be suppressed when the batch is blocked; got:\n%s", out)
	}
}

// TestPrintProgressBlock_SurfacesVerifyTerminalStates pins that plans halted at
// the verify gate render in `springfield status` exactly like their review
// counterparts: verify-needs-human (StatusNeedsHuman) under "Needs human:" and
// verify-errored (StatusFailed) under "Failed:". These map to the shared
// conductor status enum, so a verify halt must not fall through the text switch
// any more than a review halt does.
func TestPrintProgressBlock_SurfacesVerifyTerminalStates(t *testing.T) {
	b := batch.Batch{
		ID:      "b",
		Title:   "T",
		PlanIDs: []string{"01", "02"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"01", "02"}}},
	}
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		"01": {Status: conductor.StatusNeedsHuman, ExitReason: "verify-needs-human", Error: "verify gate halted"},
		"02": {Status: conductor.StatusFailed, ExitReason: "verify-errored", Error: "verify command runner failed"},
	}}

	var buf bytes.Buffer
	printProgressBlock(&buf, b, state, false, nil)
	out := buf.String()

	for _, want := range []string{"Needs human: 01", "Failed: 02"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in progress block; got:\n%s", want, out)
		}
	}
}

// TestPrintProgressBlock_NextSuppressedWhenBlocked pins that "Next:" — a hint
// about what runs next — only appears when the queue can actually advance: when
// something is running, or when nothing is blocking. A blocked batch
// (stalled/failed/needs-human present with nothing running) suppresses it so
// the text never implies forward progress the batch cannot make.
func TestPrintProgressBlock_NextSuppressedWhenBlocked(t *testing.T) {
	mk := func(plans map[string]*conductor.PlanState, ids ...string) (batch.Batch, *conductor.State) {
		return batch.Batch{
				ID: "b", Title: "T", PlanIDs: ids,
				Phases: []batch.Phase{{Mode: batch.PhaseSerial, Plans: ids}},
			},
			&conductor.State{Plans: plans}
	}

	t.Run("running_shows_next", func(t *testing.T) {
		b, st := mk(map[string]*conductor.PlanState{
			"01": {Status: conductor.StatusRunning},
			"02": {Status: conductor.StatusPending},
		}, "01", "02")
		var buf bytes.Buffer
		printProgressBlock(&buf, b, st, true, nil) // live → 01 running
		if !strings.Contains(buf.String(), "Next: 02") {
			t.Fatalf("running batch must show Next: 02; got:\n%s", buf.String())
		}
	})

	t.Run("clean_pending_shows_next", func(t *testing.T) {
		b, st := mk(map[string]*conductor.PlanState{
			"02": {Status: conductor.StatusPending},
		}, "01", "02") // 01 has no state → pending
		var buf bytes.Buffer
		printProgressBlock(&buf, b, st, false, nil)
		if !strings.Contains(buf.String(), "Next: 01") {
			t.Fatalf("unblocked all-pending batch must show Next: 01; got:\n%s", buf.String())
		}
	})

	t.Run("failed_blocks_next", func(t *testing.T) {
		b, st := mk(map[string]*conductor.PlanState{
			"01": {Status: conductor.StatusFailed},
			"02": {Status: conductor.StatusPending},
		}, "01", "02")
		var buf bytes.Buffer
		printProgressBlock(&buf, b, st, false, nil)
		if strings.Contains(buf.String(), "Next:") {
			t.Fatalf("failed batch must suppress Next; got:\n%s", buf.String())
		}
	})

	// A completed-but-not-integrated ("done") plan blocks the sequential queue:
	// the scheduler stays on its phase until it integrates, so the pending
	// sibling is not actually next. Next must be suppressed.
	t.Run("done_not_integrated_blocks_next", func(t *testing.T) {
		b, st := mk(map[string]*conductor.PlanState{
			"01": {Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeRefused}},
			"02": {Status: conductor.StatusPending},
		}, "01", "02")
		var buf bytes.Buffer
		printProgressBlock(&buf, b, st, false, nil)
		if strings.Contains(buf.String(), "Next:") {
			t.Fatalf("done-but-not-integrated batch must suppress Next; got:\n%s", buf.String())
		}
	})
}

// TestPrintProgressBlock_MergedCountedNotNamed pins the intentional handling of
// merged plans in a non-all-done batch: they are counted in the "X/Y
// integrated" tally, never listed by name in a per-plan line. Guards against a
// future switch change that accidentally surfaces a "Merged:" line.
func TestPrintProgressBlock_MergedCountedNotNamed(t *testing.T) {
	b := batch.Batch{
		ID: "b", Title: "T", PlanIDs: []string{"01", "02"},
		Phases: []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"01", "02"}}},
	}
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		"01": {
			Status:  conductor.StatusCompleted,
			Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded, SourceSyncStatus: "synced"},
			Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
		},
		"02": {Status: conductor.StatusPending},
	}}
	var buf bytes.Buffer
	printProgressBlock(&buf, b, state, false, nil)
	out := buf.String()
	if !strings.Contains(out, "Plans: 1/2 integrated") {
		t.Fatalf("want Plans: 1/2 integrated; got:\n%s", out)
	}
	if strings.Contains(out, "01") && (strings.Contains(out, "Merged") || strings.Contains(out, "Done") || strings.Contains(out, "Current") || strings.Contains(out, "Stalled")) {
		t.Fatalf("merged plan 01 must not be named in a per-plan line; got:\n%s", out)
	}
	if !strings.Contains(out, "Next: 02") {
		t.Fatalf("want Next: 02 (batch not blocked); got:\n%s", out)
	}
}

func TestStatusRollupAllDone(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"01": map[string]any{
				"status": "completed",
				"merge": map[string]any{
					"status":             "succeeded",
					"source_sync_status": "synced",
				},
				"cleanup": map[string]any{"status": "succeeded"},
			},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Plans: 1/1 integrated") {
		t.Fatalf("expected 1/1 rollup:\n%s", out)
	}
	if !strings.Contains(out, "Status: complete") {
		t.Fatalf("expected Status: complete:\n%s", out)
	}
}

func TestStatusRollupDegradesWhenStateLoadFails(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")
	// Corrupt state.json so LoadProjectRaw fails.
	stateFile := filepath.Join(root, ".springfield", "execution", "state.json")
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(stateFile, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status should degrade not error: %v", err)
	}
	if !strings.Contains(out, "Batch: batch-001") {
		t.Fatalf("expected batch header to still render:\n%s", out)
	}
	if !strings.Contains(out, "[warn]") {
		t.Fatalf("expected stderr warn line:\n%s", out)
	}
	if strings.Contains(out, "Plans: ") && strings.Contains(out, "integrated") {
		t.Fatalf("rollup should be omitted when state load fails:\n%s", out)
	}
}

func TestStatusOrphanedBatchKeepsRecoveryGuidance(t *testing.T) {
	root := newStatusRoot(t)
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "ghost-batch"}); err != nil {
		t.Fatalf("write run: %v", err)
	}

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "orphaned") {
		t.Fatalf("expected orphan guidance:\n%s", out)
	}
	if !strings.Contains(out, "springfield recover") {
		t.Fatalf("expected recover hint:\n%s", out)
	}
}

func TestStatusReportsCompletedAndFailedTruthfully(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "feature-b", "path": ".springfield/plans/feature.md", "order": 2},
	})
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"feature-a": map[string]any{"status": "completed"},
			"feature-b": map[string]any{"status": "failed", "error": "boom"},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "feature-a  completed") {
		t.Fatalf("expected feature-a completed:\n%s", out)
	}
	if !strings.Contains(out, "feature-b  failed") {
		t.Fatalf("expected feature-b failed:\n%s", out)
	}
}

func TestStatusRewritesStaleRunningPlanToInterruptedAndGuidesResume(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "feature-b", "path": ".springfield/plans/feature.md", "order": 2},
	})
	wt := filepath.Join(root, ".worktrees", "feature-a")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"feature-a": map[string]any{
				"status":        "running",
				"attempts":      1,
				"worktree_path": wt,
				"branch":        "springfield/feature-a",
				"base_ref":      "main",
				"base_head":     "aaaaaaaa",
			},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "feature-a  interrupted") {
		t.Fatalf("expected interrupted status:\n%s", out)
	}
	if !strings.Contains(out, "resume interrupted plan \"feature-a\"") {
		t.Fatalf("expected resume guidance:\n%s", out)
	}
	if !strings.Contains(out, "interrupted-process-exit") {
		t.Fatalf("expected interruption exit reason:\n%s", out)
	}

	stateBytes, err := os.ReadFile(filepath.Join(root, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(stateBytes), "\"status\": \"interrupted\"") {
		t.Fatalf("state not rewritten to interrupted:\n%s", stateBytes)
	}
}

func TestStatusDoesNotRewriteRunningPlanWhileStartLockHeld(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "feature-a", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"feature-a": map[string]any{
				"status":        "running",
				"attempts":      1,
				"worktree_path": filepath.Join(root, ".worktrees", "feature-a"),
				"branch":        "springfield/feature-a",
				"base_ref":      "main",
				"base_head":     "aaaaaaaa",
			},
		},
	})

	lk, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lk.Release()

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "feature-a  running") {
		t.Fatalf("expected live running status:\n%s", out)
	}
	if !strings.Contains(out, "already running") {
		t.Fatalf("expected live-run guidance:\n%s", out)
	}

	stateBytes, err := os.ReadFile(filepath.Join(root, ".springfield", "execution", "state.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(stateBytes), "\"status\": \"running\"") {
		t.Fatalf("state was mutated despite held lock:\n%s", stateBytes)
	}
}

// TestStatusSuppressesStaleFatalErrorAfterRecover pins D1: once the failed plan
// the fatal error refers to has been recovered (back to pending), the stale
// batch-level "Fatal error" line must not render beside the fresh gate.
func TestStatusSuppressesStaleFatalErrorAfterRecover(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
	})
	writeActiveBatch(t, root, "batch-001", "Active Batch")
	// A prior failure recorded a batch-level fatal error...
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "batch-001", FatalError: "plan 01 crashed: boom"}); err != nil {
		t.Fatalf("write run: %v", err)
	}
	// ...then recovery reset plan 01 back to pending.
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"01": map[string]any{"status": "pending"},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(out, "Fatal error") {
		t.Fatalf("stale fatal error should be suppressed after recover:\n%s", out)
	}
}

// TestStatusKeepsFatalErrorWhileAnotherPlanStillFailed pins the multi-plan half
// of D1: recovering one plan must not hide an error while another plan in the
// batch is still failed.
func TestStatusKeepsFatalErrorWhileAnotherPlanStillFailed(t *testing.T) {
	root := newStatusRoot(t)
	writeStatusPlan(t, root, "feature.md")
	writeStatusConfig(t, root, []map[string]any{
		{"id": "01", "path": ".springfield/plans/feature.md", "order": 1},
		{"id": "02", "path": ".springfield/plans/feature.md", "order": 2},
	})
	writeActiveBatchN(t, root, "batch-001", "Active Batch", []string{"01", "02"})
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "batch-001", FatalError: "plan 02 crashed: boom"}); err != nil {
		t.Fatalf("write run: %v", err)
	}
	// 01 recovered to pending, 02 still failed.
	writeStatusState(t, root, map[string]any{
		"plans": map[string]any{
			"01": map[string]any{"status": "pending"},
			"02": map[string]any{"status": "failed", "error": "boom"},
		},
	})

	out, err := runStatusIn(root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "Fatal error") {
		t.Fatalf("fatal error must remain while a plan is still failed:\n%s", out)
	}
}

// --- helpers ---

func newStatusRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	body := `[project]
agent_priority = ["claude"]
`
	if err := os.WriteFile(filepath.Join(root, "springfield.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	return root
}

func writeStatusPlan(t *testing.T, root, file string) {
	t.Helper()
	dir := filepath.Join(root, ".springfield", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("# plan"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
}

func writeStatusConfig(t *testing.T, root string, planUnits []map[string]any) {
	t.Helper()
	cfg := map[string]any{
		"plans_dir":                    ".springfield/plans",
		"worktree_base":                ".worktrees",
		"max_retries":                  1,
		"single_workstream_iterations": 10,
		"single_workstream_timeout":    600,
		"tool":                         "claude",
		"plan_units":                   planUnits,
	}
	writeStatusJSON(t, root, "execution/config.json", cfg)
}

func writeStatusState(t *testing.T, root string, state map[string]any) {
	t.Helper()
	writeStatusJSON(t, root, "execution/state.json", state)
}

func writeStatusJSON(t *testing.T, root, rel string, value any) {
	t.Helper()
	full := filepath.Join(root, ".springfield", rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", full, err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
}

func writeActiveBatch(t *testing.T, root, batchID, title string) {
	t.Helper()
	writeActiveBatchN(t, root, batchID, title, []string{"01"})
}

func writeActiveBatchN(t *testing.T, root, batchID, title string, planIDs []string) {
	t.Helper()
	paths, err := batch.NewPaths(root, batchID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	phases := make([]batch.Phase, len(planIDs))
	for i, id := range planIDs {
		phases[i] = batch.Phase{Mode: batch.PhaseSerial, Plans: []string{id}}
	}
	b := batch.Batch{ID: batchID, Title: title, Phases: phases, PlanIDs: planIDs}
	if err := batch.WriteBatch(paths, b, "source", nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: batchID}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
}

func writeActiveBatchParallel(t *testing.T, root, batchID, title string, planIDs []string) {
	t.Helper()
	paths, err := batch.NewPaths(root, batchID)
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}
	b := batch.Batch{
		ID:      batchID,
		Title:   title,
		Phases:  []batch.Phase{{Mode: batch.PhaseParallel, Plans: planIDs}},
		PlanIDs: planIDs,
	}
	if err := batch.WriteBatch(paths, b, "source", nil); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: batchID}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
}

func runStatusIn(root string) (string, error) {
	cmd := NewStatusCommand()
	cmd.SetArgs([]string{"--dir", root})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}
