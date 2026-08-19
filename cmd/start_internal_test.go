package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/batchexec"
	"springfield/internal/features/notify"
)

// fakeNotifier records every Event it receives without touching the OS, so
// tests can assert the batch seam fires the right terminal-state event.
type fakeNotifier struct {
	events []notify.Event
}

func (f *fakeNotifier) Notify(e notify.Event) { f.events = append(f.events, e) }

// TestBatchPlanRunnerIsTerminal verifies the batchexec terminal contract at
// the adapter boundary: only a FULLY INTEGRATED plan (merge succeeded +
// cleanup succeeded) is terminal. In particular StatusCompleted alone is NOT
// terminal — such a plan must still be dispatched so RunPlan can drive the
// integration-only re-entry path (a crash between execution and integration
// would otherwise orphan the plan).
func TestBatchPlanRunnerIsTerminal(t *testing.T) {
	project := &conductor.Project{
		State: &conductor.State{
			Plans: map[string]*conductor.PlanState{
				"integrated": {
					Status:  conductor.StatusCompleted,
					Merge:   &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
					Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
				},
				// Completed with NO merge record: IsIntegrated treats this
				// as integrated (legacy/no-merge-ledger semantics) — same as
				// the old batchNextPlanID predicate.
				"completed-no-merge-record": {
					Status: conductor.StatusCompleted,
				},
				// Completed but integration incomplete (merge pending): the
				// crash-between-execution-and-integration re-entry case.
				"completed-not-integrated": {
					Status: conductor.StatusCompleted,
					Merge:  &conductor.MergeOutcome{Status: conductor.MergePending},
				},
				// Merge succeeded but cleanup never durably recorded.
				"completed-cleanup-missing": {
					Status: conductor.StatusCompleted,
					Merge:  &conductor.MergeOutcome{Status: conductor.MergeSucceeded},
				},
				"running": {
					Status: conductor.StatusRunning,
				},
			},
		},
	}
	r := &batchPlanRunner{project: project}

	if !r.IsTerminal("integrated") {
		t.Errorf("integrated plan must be terminal")
	}
	if !r.IsTerminal("completed-no-merge-record") {
		t.Errorf("completed plan with no merge record is integrated (legacy semantics) — must be terminal")
	}
	if r.IsTerminal("completed-not-integrated") {
		t.Errorf("completed-but-not-integrated plan must NOT be terminal (needs integration re-entry)")
	}
	if r.IsTerminal("completed-cleanup-missing") {
		t.Errorf("merge-succeeded-but-cleanup-unrecorded plan must NOT be terminal")
	}
	if r.IsTerminal("running") {
		t.Errorf("running plan must not be terminal")
	}
	if r.IsTerminal("unknown") {
		t.Errorf("unknown plan must not be terminal")
	}
}

// TestRunBatchWithContextCancelledReturnsContextCanceled verifies that
// runBatchWithContext returns context.Canceled (not nil) when the context is
// already cancelled before the loop runs. The caller must not archive or clear
// run.json on this return value.
func TestRunBatchWithContextCancelledReturnsContextCanceled(t *testing.T) {
	root := t.TempDir()

	// Minimal springfield.toml with agent config.
	if err := os.WriteFile(
		root+"/springfield.toml",
		[]byte("[project]\nagent_priority = [\"claude\"]\n"),
		0o644,
	); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	// Write run.json so we can verify it's untouched.
	run := batch.Run{ActiveBatchID: "test-batch"}
	if err := batch.WriteRun(root, run); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	b := batch.Batch{
		ID:      "test-batch",
		Title:   "Test",
		PlanIDs: []string{"plan-a"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling

	_, err := runBatchWithContext(ctx, root, &run, b, io.Discard, "", 0, false, "", 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// run.json must still exist (not cleared).
	if _, statErr := os.Stat(batch.RunPath(root)); statErr != nil {
		t.Errorf("run.json should still exist after interrupt: %v", statErr)
	}
}

// TestPlanDirTamperGuardSnapshotsRunJSON verifies that planDirTamperGuard
// snapshots run.json and detects deletion or modification as tamper.
func TestPlanDirTamperGuardSnapshotsRunJSON(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(root, ".springfield", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir planDir: %v", err)
	}
	// Write a run.json before snapshot.
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "my-batch"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	guard := &planDirTamperGuard{planDir: planDir, controlRoot: root}
	if err := guard.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	t.Run("delete run.json is tamper", func(t *testing.T) {
		if err := os.Remove(batch.RunPath(root)); err != nil {
			t.Fatalf("remove run.json: %v", err)
		}
		reason, err := guard.Detect()
		if err != nil {
			t.Fatalf("Detect: %v", err)
		}
		if reason == "" {
			t.Fatal("expected tamper reason for deleted run.json, got empty")
		}
		// Restore puts it back.
		if err := guard.Restore(); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		data, err := os.ReadFile(batch.RunPath(root))
		if err != nil {
			t.Fatalf("run.json missing after Restore: %v", err)
		}
		if !bytes.Contains(data, []byte("my-batch")) {
			t.Errorf("restored run.json missing expected content: %s", data)
		}
	})

	// Re-snapshot after restore so the next sub-test starts from valid state.
	if err := guard.Snapshot(); err != nil {
		t.Fatalf("re-Snapshot: %v", err)
	}

	t.Run("modify run.json is tamper", func(t *testing.T) {
		if err := os.WriteFile(batch.RunPath(root), []byte(`{"active_batch_id":"corrupted"}`), 0o644); err != nil {
			t.Fatalf("write corrupted run.json: %v", err)
		}
		reason, err := guard.Detect()
		if err != nil {
			t.Fatalf("Detect: %v", err)
		}
		if reason == "" {
			t.Fatal("expected tamper reason for modified run.json, got empty")
		}
		// Restore puts it back to original bytes.
		if err := guard.Restore(); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		data, err := os.ReadFile(batch.RunPath(root))
		if err != nil {
			t.Fatalf("run.json missing after Restore: %v", err)
		}
		if !bytes.Contains(data, []byte("my-batch")) {
			t.Errorf("restored run.json missing expected content: %s", data)
		}
	})
}

// TestPlanDirTamperGuardRunJSONAbsentBeforeSnapshot verifies that when run.json
// does not exist at Snapshot time, deleting it (noop) is not tamper, and creating
// it is tamper (guarded to keep the Snapshot semantics consistent).
func TestPlanDirTamperGuardRunJSONAbsentBeforeSnapshot(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(root, ".springfield", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir planDir: %v", err)
	}
	// No run.json written — doesn't exist at Snapshot time.

	guard := &planDirTamperGuard{planDir: planDir, controlRoot: root}
	if err := guard.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// With no run.json before or after, Detect must report no tamper.
	reason, err := guard.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if reason != "" {
		t.Errorf("expected no tamper when run.json absent in both snapshot and current, got %q", reason)
	}

	// If agent CREATES run.json (it didn't exist before), that's tamper.
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "injected"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	reason, err = guard.Detect()
	if err != nil {
		t.Fatalf("Detect after creation: %v", err)
	}
	if reason == "" {
		t.Fatal("expected tamper when run.json created by agent, got empty")
	}
	// Restore must delete the injected file.
	if err := guard.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, statErr := os.Stat(batch.RunPath(root)); !os.IsNotExist(statErr) {
		t.Errorf("expected run.json to be deleted by Restore, stat error: %v", statErr)
	}
}

// TestRunBatchWithContextMissingExecutionConfigFails verifies that when a batch
// references plans but the conductor execution config is absent (or empty),
// runBatchWithContext returns an error and does NOT archive/clear the batch.
func TestRunBatchWithContextMissingExecutionConfigFails(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"),
		0o644,
	); err != nil {
		t.Fatalf("write toml: %v", err)
	}

	// Write run.json.
	run := batch.Run{ActiveBatchID: "test-batch"}
	if err := batch.WriteRun(root, run); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	// Batch references a plan but we intentionally DO NOT write execution config.
	b := batch.Batch{
		ID:      "test-batch",
		Title:   "Test",
		PlanIDs: []string{"plan-a"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}}},
	}

	ctx := context.Background()
	result, err := runBatchWithContext(ctx, root, &run, b, io.Discard, "", 0, false, "", 1)
	if err == nil {
		t.Fatal("expected error when execution config is missing, got nil")
	}
	if result.Error == "" {
		t.Fatal("expected BatchRunResult.Error to be set")
	}

	// Batch must still be present (not archived/cleared).
	if _, statErr := os.Stat(batch.RunPath(root)); statErr != nil {
		t.Errorf("run.json should still exist after config-missing failure: %v", statErr)
	}
}

// TestRunBatchWithContextMalformedLocalTOMLFails verifies that a present-but-
// malformed springfield.local.toml is surfaced as a wrapped error from
// runBatchWithContext, so operators see a clear "load springfield.local.toml"
// prefix instead of a bare TOML decode error.
func TestRunBatchWithContextMalformedLocalTOMLFails(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(
		filepath.Join(root, "springfield.toml"),
		[]byte("[project]\nagent_priority = [\"claude\"]\n"),
		0o644,
	); err != nil {
		t.Fatalf("write toml: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "springfield.local.toml"),
		[]byte("[review\nenabled = true\n"),
		0o644,
	); err != nil {
		t.Fatalf("write local toml: %v", err)
	}

	run := batch.Run{ActiveBatchID: "test-batch"}
	if err := batch.WriteRun(root, run); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	b := batch.Batch{
		ID:      "test-batch",
		Title:   "Test",
		PlanIDs: []string{"plan-a"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"plan-a"}}},
	}

	_, err := runBatchWithContext(context.Background(), root, &run, b, io.Discard, "", 0, false, "", 1)
	if err == nil {
		t.Fatal("expected error from malformed local toml, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("load springfield.local.toml")) {
		t.Errorf("error %q missing wrap prefix \"load springfield.local.toml\"", err.Error())
	}
}

// TestRunCursorTracksActivePlanIDs locks down the dispatch/settle bookkeeping
// behind run.json's active_plan_ids: every transition is checkpointed, settle
// removes exactly the settled plan, and an unknown id is a no-op.
func TestRunCursorTracksActivePlanIDs(t *testing.T) {
	root := t.TempDir()
	run := batch.Run{ActiveBatchID: "b1"}
	c := &runCursor{root: root, run: &run}

	assertActive := func(want ...string) {
		t.Helper()
		got, ok, err := batch.ReadRun(root)
		if err != nil || !ok {
			t.Fatalf("ReadRun: ok=%v err=%v", ok, err)
		}
		if len(got.ActivePlanIDs) != len(want) {
			t.Fatalf("ActivePlanIDs = %v, want %v", got.ActivePlanIDs, want)
		}
		for i := range want {
			if got.ActivePlanIDs[i] != want[i] {
				t.Fatalf("ActivePlanIDs = %v, want %v", got.ActivePlanIDs, want)
			}
		}
	}

	c.dispatch("alpha")
	c.dispatch("beta")
	assertActive("alpha", "beta")

	c.settle("alpha")
	assertActive("beta")

	// Settling an id that is not in flight must not corrupt the list.
	c.settle("ghost")
	assertActive("beta")

	c.settle("beta")
	assertActive()

	// The cursor shares the caller's Run object (pointer), so the drained
	// state is what the caller re-persists on its post-run write paths
	// (cost-cap/failure) — a stale copy here would resurrect pre-dispatch
	// active_plan_ids in run.json.
	if len(run.ActivePlanIDs) != 0 {
		t.Errorf("caller-visible Run not drained: ActivePlanIDs = %v", run.ActivePlanIDs)
	}
}

// TestBatchPlanRunnerProgressWriterSelection verifies the phase-static writer
// choice in RunPlan's glue: concurrent dispatch prefixes every line (including
// a flushed trailing partial) through the shared writer; sequential dispatch
// writes through untouched.
func TestBatchPlanRunnerProgressWriterSelection(t *testing.T) {
	var buf bytes.Buffer
	r := &batchPlanRunner{progress: &buf, progressShared: &syncLineWriter{w: &buf}}

	w, flush := r.planProgress("alpha", batchexec.RunInfo{Concurrent: true})
	fmt.Fprintf(w, "hello\npartial")
	flush()
	if got := buf.String(); got != "[alpha] hello\n[alpha] partial\n" {
		t.Fatalf("concurrent output = %q, want prefixed lines", got)
	}

	buf.Reset()
	w, flush = r.planProgress("alpha", batchexec.RunInfo{Concurrent: false})
	fmt.Fprintf(w, "hello\n")
	flush()
	if got := buf.String(); got != "hello\n" {
		t.Fatalf("sequential output = %q, want raw passthrough", got)
	}
}

// TestBatchPlanRunnerTamperGuardConcurrentIsolation: while a concurrent
// plan's agent runs, the scheduler legitimately rewrites run.json (cursor
// checkpoints on sibling dispatch/settle) and sibling plans legitimately
// write their own control-plane files (progress.md appends, prd.json
// story-pass rewrites) in the shared .springfield/plans tree. The per-plan
// guard for a concurrent dispatch must therefore watch ONLY the plan's own
// subtree and skip run.json — otherwise every parallel plan deterministically
// false-trips tamper detection and Restore reverts sibling state. Sequential
// dispatches keep the full whole-tree + run.json guard.
func TestBatchPlanRunnerTamperGuardConcurrentIsolation(t *testing.T) {
	root := t.TempDir()
	alphaDir := filepath.Join(root, ".springfield", "plans", "alpha")
	betaDir := filepath.Join(root, ".springfield", "plans", "beta")
	for _, d := range []string{alphaDir, betaDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	run := batch.Run{ActiveBatchID: "b1"}
	if err := batch.WriteRun(root, run); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	r := &batchPlanRunner{
		root: root,
		project: &conductor.Project{Config: &conductor.Config{PlanUnits: []conductor.PlanUnit{
			{ID: "alpha", Path: ".springfield/plans/alpha/prd.json", Order: 1},
			{ID: "beta", Path: ".springfield/plans/beta/prd.json", Order: 2},
		}}},
	}
	cursor := &runCursor{root: root, run: &run}

	g := r.tamperGuard("alpha", batchexec.RunInfo{Concurrent: true})
	if err := g.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Scheduler checkpoint + sibling control-plane writes land mid-window:
	// legitimate, not tamper.
	cursor.dispatch("beta")
	if err := os.WriteFile(filepath.Join(betaDir, "progress.md"), []byte("iteration 1 start\n"), 0o644); err != nil {
		t.Fatalf("write sibling progress: %v", err)
	}
	reason, err := g.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if reason != "" {
		t.Fatalf("concurrent guard flagged legitimate sibling/scheduler writes as tamper: %q", reason)
	}
	// Tampering with the plan's OWN subtree is still caught.
	if err := os.WriteFile(filepath.Join(alphaDir, "evil.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reason, err = g.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if reason == "" {
		t.Fatal("concurrent guard must still detect tampering in the plan's own dir")
	}
	// Restore must only touch the plan's own subtree — sibling state survives.
	if err := g.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alphaDir, "evil.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Restore did not remove tampered file from own dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(betaDir, "progress.md")); err != nil {
		t.Errorf("Restore reverted sibling state: %v", err)
	}

	// Sequential dispatch keeps the full whole-tree + run.json guard.
	g2 := r.tamperGuard("alpha", batchexec.RunInfo{Concurrent: false})
	if err := g2.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	cursor.settle("beta")
	reason, err = g2.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if reason == "" {
		t.Fatal("sequential guard must detect run.json changes")
	}
}

// TestResolveMaxParallelFlagPrecedence: --max-parallel > 0 overrides the
// configured value; 0 (flag omitted) falls back to config.
func TestResolveMaxParallelFlagPrecedence(t *testing.T) {
	if got := resolveMaxParallel(0, 3); got != 3 {
		t.Errorf("flag 0: got %d, want configured 3", got)
	}
	if got := resolveMaxParallel(2, 3); got != 2 {
		t.Errorf("flag 2: got %d, want flag override 2", got)
	}
	if got := resolveMaxParallel(1, 3); got != 1 {
		t.Errorf("flag 1: got %d, want 1 (concurrency disabled)", got)
	}
	// Negative values are an explicit override, clamped to sequential — the
	// same semantics the config resolver gives max_parallel <= 1.
	if got := resolveMaxParallel(-1, 3); got != 1 {
		t.Errorf("flag -1: got %d, want 1 (clamped to sequential)", got)
	}
}

// TestNotifyBatchOutcomeFiresRightEventPerTerminalState pins the notifier seam:
// each batch terminal state (needs-human, complete, failed, cost-capped) fires
// exactly one Event of the matching Kind, via a fake Notifier that makes no OS
// call. Priority ties (a pause riding alongside a halt Error) resolve to the
// pause, matching the RunE report order.
func TestNotifyBatchOutcomeFiresRightEventPerTerminalState(t *testing.T) {
	cases := []struct {
		name     string
		result   BatchRunResult
		wantKind notify.Kind
		assert   func(t *testing.T, e notify.Event)
	}{
		{
			name:     "complete",
			result:   BatchRunResult{},
			wantKind: notify.Complete,
		},
		{
			name:     "failed",
			result:   BatchRunResult{Error: "boom"},
			wantKind: notify.Failed,
			assert: func(t *testing.T, e notify.Event) {
				if e.Detail != "boom" {
					t.Errorf("failed event Detail = %q, want %q", e.Detail, "boom")
				}
			},
		},
		{
			name:     "cost-capped",
			result:   BatchRunResult{CostCapped: true, SpendUSD: 12.5},
			wantKind: notify.CostCapped,
			assert: func(t *testing.T, e notify.Event) {
				if e.SpendUSD != 12.5 {
					t.Errorf("cost-capped event SpendUSD = %v, want 12.5", e.SpendUSD)
				}
			},
		},
		{
			name: "needs-human outranks the halt error it rides alongside",
			// A needs-human pause halts the batch, so Error is also set; the
			// seam must still surface needs-human, not failure.
			result:   BatchRunResult{NeedsHuman: true, Error: "plan paused"},
			wantKind: notify.NeedsHuman,
		},
		{
			name: "cost-cap outranks a coexisting failure",
			// A cost-cap pause draining a failed sibling: pause wins (resumable).
			result:   BatchRunResult{CostCapped: true, Error: "sibling failed"},
			wantKind: notify.CostCapped,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeNotifier{}
			notifyBatchOutcome(fake, "batch-1", tc.result)
			if len(fake.events) != 1 {
				t.Fatalf("got %d events, want exactly 1: %+v", len(fake.events), fake.events)
			}
			e := fake.events[0]
			if e.Kind != tc.wantKind {
				t.Fatalf("Kind = %d, want %d", e.Kind, tc.wantKind)
			}
			if e.BatchID != "batch-1" {
				t.Errorf("BatchID = %q, want %q", e.BatchID, "batch-1")
			}
			if tc.assert != nil {
				tc.assert(t, e)
			}
		})
	}
}
