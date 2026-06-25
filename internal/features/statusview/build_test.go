package statusview_test

import (
	"encoding/json"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/cost"
	"springfield/internal/features/statusview"
)

func TestIdle_EnvelopeShape(t *testing.T) {
	v := statusview.Idle()
	if v.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", v.SchemaVersion)
	}
	if v.State != "idle" {
		t.Fatalf("state = %q, want idle", v.State)
	}
	if v.Summary == "" {
		t.Fatal("summary must be present")
	}
	if v.Batch != nil || v.Progress != nil || v.Spend != nil || v.Plans != nil || v.Flags != nil {
		t.Fatal("idle must null out batch/progress/spend/flags/plans")
	}
	// Absent sections marshal as explicit null, never missing keys.
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"batch":null`, `"progress":null`, `"spend":null`, `"flags":null`, `"plans":null`, `"state":"idle"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("idle JSON missing %s; got %s", key, b)
		}
	}
}

func TestOrphan_FromRun(t *testing.T) {
	run := batch.Run{ActiveBatchID: "batch-x", CostCapped: true, FatalError: "boom"}
	v := statusview.Orphan(run)
	if v.State != "orphan" {
		t.Fatalf("state = %q, want orphan", v.State)
	}
	if v.Batch == nil || v.Batch.ID != "batch-x" {
		t.Fatalf("orphan batch id = %+v, want batch-x", v.Batch)
	}
	if v.Flags == nil || !v.Flags.CostCapped {
		t.Fatal("orphan must carry cost_capped flag")
	}
	if v.Flags.FatalError == nil || *v.Flags.FatalError != "boom" {
		t.Fatal("orphan must carry fatal_error")
	}
	if v.Plans != nil {
		t.Fatal("orphan must null out plans")
	}
}

func TestComposeStatus_Totality(t *testing.T) {
	// Every internal PlanStatus must map to exactly one public value. The `live`
	// dimension gates running vs stalled for started-but-non-terminal plans (a
	// live springfield process owning the control-plane lock). Terminal/pending
	// states are liveness-independent.
	cases := []struct {
		name string
		ps   *conductor.PlanState
		live bool
		want string
	}{
		{"pending", &conductor.PlanState{Status: conductor.StatusPending}, true, statusview.StatusPending},
		// Started-but-non-terminal: running when a live process owns the lock, else stalled.
		{"running+live->running", &conductor.PlanState{Status: conductor.StatusRunning}, true, statusview.StatusRunning},
		{"running+dead->stalled", &conductor.PlanState{Status: conductor.StatusRunning}, false, statusview.StatusStalled},
		{"interrupted+live->running", &conductor.PlanState{Status: conductor.StatusInterrupted}, true, statusview.StatusRunning},
		{"interrupted+dead->stalled", &conductor.PlanState{Status: conductor.StatusInterrupted}, false, statusview.StatusStalled},
		{"failed", &conductor.PlanState{Status: conductor.StatusFailed}, true, statusview.StatusFailed},
		{"needs-human", &conductor.PlanState{Status: conductor.StatusNeedsHuman}, true, statusview.StatusNeedsHuman},
		// Finding 1: Merge==nil legacy path → IsIntegrated() returns true → merged.
		{"completed-no-merge-record->merged", &conductor.PlanState{Status: conductor.StatusCompleted}, true, statusview.StatusMerged},
		{"completed-refused->done", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeRefused}}, true, statusview.StatusDone},
		// Finding 1: merged requires IsIntegrated() — MergeSucceeded + CleanupSucceeded + SourceSyncStatus not failed.
		{"completed-succeeded->merged", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeSucceeded}, Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded}}, true, statusview.StatusMerged},
		// Finding 1: cleanup-failed means not integrated → done.
		{"completed-succeeded-cleanup-failed->done", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeSucceeded}, Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupFailed}}, true, statusview.StatusDone},
		// Finding 1: source-sync-failed means not integrated → done.
		{"completed-succeeded-source-sync-failed->done", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeSucceeded, SourceSyncStatus: "failed"}, Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded}}, true, statusview.StatusDone},
		// Finding 2: MergeSucceeded+Cleanup==nil → IsIntegrated returns false → done.
		{"completed-succeeded-cleanup-nil->done", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeSucceeded}}, true, statusview.StatusDone},
		// Finding 2: MergePending and MergeFailed are done, not merged.
		{"completed-merge-pending->done", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergePending}}, true, statusview.StatusDone},
		{"completed-merge-failed->done", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeFailed}}, true, statusview.StatusDone},
		{"nil->pending", nil, true, statusview.StatusPending},
		{"unknown->needs-human", &conductor.PlanState{Status: conductor.PlanStatus("future-state")}, true, statusview.StatusNeedsHuman},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statusview.ComposeStatus(tc.ps, tc.live)
			if got != tc.want {
				t.Fatalf("composeStatus(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func strp(s string) *string { return &s }

func TestBuildPlan_HaltAndBase(t *testing.T) {
	ps := &conductor.PlanState{
		Status:       conductor.StatusNeedsHuman,
		ExitReason:   "review-needs-human",
		Error:        "pre-merge review halted: missing tests (full findings in evidence)",
		Branch:       "springfield/feat-a",
		BaseRef:      "springfield/batch-001",
		BaseHead:     "abc1234",
		Attempts:     2,
		EvidencePath: ".springfield/execution/plans/feat-a/evidence",
	}
	pv := statusview.BuildPlanForTest("feat-a", "Add feature A", ps, false)

	if pv.ID != "feat-a" || pv.Title != "Add feature A" {
		t.Fatalf("id/title = %q/%q", pv.ID, pv.Title)
	}
	if pv.Status != statusview.StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human", pv.Status)
	}
	if pv.BaseBranch != "springfield/batch-001" || pv.BaseHead != "abc1234" {
		t.Fatalf("base = %q@%q", pv.BaseBranch, pv.BaseHead)
	}
	if pv.Review.Verdict == nil || *pv.Review.Verdict != "halt" {
		t.Fatal("halt verdict must be set from ExitReason")
	}
	if pv.Review.Reason == nil || *pv.Review.Reason != ps.Error {
		t.Fatal("review reason must come from PlanState.Error excerpt")
	}
	if pv.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", pv.Attempt)
	}
}

func TestBuildPlan_TitleFallsBackToID(t *testing.T) {
	pv := statusview.BuildPlanForTest("feat-a", "", &conductor.PlanState{Status: conductor.StatusPending}, true)
	if pv.Title != "feat-a" {
		t.Fatalf("title = %q, want id fallback feat-a", pv.Title)
	}
	if pv.Review.Verdict != nil || pv.Merge.Status != nil {
		t.Fatal("non-halt/non-merged plan must have null verdict and null merge status")
	}
}

func TestBuildPlan_MergeRefused(t *testing.T) {
	ps := &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge:  &conductor.MergeOutcome{Status: conductor.MergeRefused, Reason: "drift detected", Error: "git: target branch moved to abc123"},
	}
	pv := statusview.BuildPlanForTest("feat-a", "A", ps, true)
	if pv.Status != statusview.StatusDone {
		t.Fatalf("status = %q, want done (refused merge is still done)", pv.Status)
	}
	if pv.Merge.Status == nil || *pv.Merge.Status != "refused" {
		t.Fatal("merge.status must surface refused")
	}
	if pv.Merge.Reason == nil || *pv.Merge.Reason != "drift detected" {
		t.Fatal("merge.reason must surface the drift reason")
	}
	// Finding 4: human-readable error message must also surface.
	if pv.Merge.Error == nil || *pv.Merge.Error != "git: target branch moved to abc123" {
		t.Fatalf("merge.error must surface the human error message; got %v", pv.Merge.Error)
	}
}

func TestActive_OnePlanProjection(t *testing.T) {
	b := batch.Batch{
		ID:      "batch-001",
		Title:   "Ship feature A",
		PlanIDs: []string{"feat-a"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"feat-a"}}},
	}
	state := &conductor.State{Plans: map[string]*conductor.PlanState{
		"feat-a": {Status: conductor.StatusRunning, Branch: "springfield/feat-a", Attempts: 1},
	}}
	in := statusview.ActiveInput{
		Batch:     b,
		Run:       batch.Run{ActiveBatchID: "batch-001"},
		State:     state,
		Units:     []conductor.PlanUnit{{ID: "feat-a", Title: "Add feature A"}},
		Rollup:    cost.Rollup{TotalUSD: 1.5, Iterations: 3, PerAdapter: map[string]float64{"claude": 1.5}},
		HasRollup: true,
		Live:      true, // a live process owns the lock → running plan reads as running
	}
	v := statusview.Active(in)

	if v.State != "active" || v.SchemaVersion != 1 {
		t.Fatalf("envelope = %s/%d", v.State, v.SchemaVersion)
	}
	if v.Batch == nil || v.Batch.Title != "Ship feature A" {
		t.Fatalf("batch = %+v", v.Batch)
	}
	if v.Spend == nil || v.Spend.TotalUSD != 1.5 {
		t.Fatalf("spend = %+v", v.Spend)
	}
	if len(v.Plans) != 1 || v.Plans[0].Title != "Add feature A" || v.Plans[0].Status != statusview.StatusRunning {
		t.Fatalf("plans = %+v", v.Plans)
	}
	if v.Progress == nil || v.Progress.Total != 1 {
		t.Fatalf("progress = %+v", v.Progress)
	}
}

func TestActive_SpendSkippedFiles(t *testing.T) {
	in := statusview.ActiveInput{
		Batch:     batch.Batch{ID: "b", PlanIDs: []string{"p"}},
		Run:       batch.Run{ActiveBatchID: "b"},
		State:     &conductor.State{Plans: map[string]*conductor.PlanState{"p": {Status: conductor.StatusPending}}},
		Rollup:    cost.Rollup{TotalUSD: 2.0, Iterations: 4, SkippedFiles: 3},
		HasRollup: true,
	}
	v := statusview.Active(in)
	if v.Spend == nil {
		t.Fatal("spend must be present when HasRollup=true")
	}
	if v.Spend.SkippedFiles != 3 {
		t.Fatalf("spend.skipped_files = %d, want 3", v.Spend.SkippedFiles)
	}
	// Verify it marshals into JSON.
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"skipped_files":3`) {
		t.Errorf("JSON missing skipped_files:3; got %s", b)
	}
}

func TestActive_NoRollupOmitsSpend(t *testing.T) {
	in := statusview.ActiveInput{
		Batch:     batch.Batch{ID: "b", PlanIDs: []string{"p"}},
		Run:       batch.Run{ActiveBatchID: "b"},
		State:     &conductor.State{Plans: map[string]*conductor.PlanState{"p": {Status: conductor.StatusPending}}},
		HasRollup: false,
	}
	v := statusview.Active(in)
	if v.Spend != nil {
		t.Fatal("spend must be null when rollup has no iterations")
	}
}

// TestActive_FatalErrorSuppression checks that the JSON active path suppresses
// a stale fatal_error when no plan is still in a halting state (matching the
// text path's batchHasFailedPlan gate).
func TestActive_FatalErrorSuppression(t *testing.T) {
	mkBatch := func(planIDs ...string) batch.Batch {
		return batch.Batch{ID: "b", PlanIDs: planIDs,
			Phases: []batch.Phase{{Mode: batch.PhaseSerial, Plans: planIDs}}}
	}
	mkState := func(status conductor.PlanStatus) *conductor.State {
		return &conductor.State{Plans: map[string]*conductor.PlanState{
			"p": {Status: status},
		}}
	}

	t.Run("stale_fatal_error_suppressed_when_no_failed_plan", func(t *testing.T) {
		// Plan has been recovered — StatusCompleted — so fatal_error is stale.
		in := statusview.ActiveInput{
			Batch:      mkBatch("p"),
			Run:        batch.Run{ActiveBatchID: "b", FatalError: "original halt reason"},
			State:      mkState(conductor.StatusCompleted),
			FatalError: "", // caller computed: batchHasFailedPlan returned false
		}
		v := statusview.Active(in)
		if v.Flags == nil {
			t.Fatal("flags must be non-nil for active batch")
		}
		if v.Flags.FatalError != nil {
			t.Fatalf("fatal_error must be null for recovered batch, got %q", *v.Flags.FatalError)
		}
	})

	t.Run("fatal_error_present_when_plan_still_failed", func(t *testing.T) {
		in := statusview.ActiveInput{
			Batch:      mkBatch("p"),
			Run:        batch.Run{ActiveBatchID: "b", FatalError: "halt reason"},
			State:      mkState(conductor.StatusFailed),
			FatalError: "halt reason", // caller computed: batchHasFailedPlan returned true
		}
		v := statusview.Active(in)
		if v.Flags == nil {
			t.Fatal("flags must be non-nil for active batch")
		}
		if v.Flags.FatalError == nil || *v.Flags.FatalError != "halt reason" {
			t.Fatalf("fatal_error must reflect halt when plan still failed, got %v", v.Flags.FatalError)
		}
	})
}

func TestActive_JSONShape(t *testing.T) {
	in := statusview.ActiveInput{
		Batch: batch.Batch{ID: "b1", Title: "T", PlanIDs: []string{"p1"}},
		Run:   batch.Run{ActiveBatchID: "b1"},
		State: &conductor.State{Plans: map[string]*conductor.PlanState{"p1": {Status: conductor.StatusPending}}},
		Units: []conductor.PlanUnit{{ID: "p1", Title: "Plan one"}},
	}
	out, err := json.MarshalIndent(statusview.Active(in), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Contract keys that Flightdeck branches on must be present.
	for _, key := range []string{
		`"schema_version": 1`, `"state": "active"`, `"plans": [`,
		`"id": "p1"`, `"status": "pending"`, `"base_branch": ""`,
		`"review": {`, `"verdict": null`, `"merge": {`, `"status": null`,
		`"integration": {`, `"state": "clean"`,
	} {
		if !strings.Contains(string(out), key) {
			t.Errorf("active JSON missing %q\n--- got ---\n%s", key, out)
		}
	}
}

// TestActive_Liveness verifies the stalled-vs-running projection end to end at
// the builder boundary: a started-but-non-terminal plan reads as running only
// when a live process owns the control-plane lock, else stalled.
func TestActive_Liveness(t *testing.T) {
	mk := func(live bool) statusview.View {
		return statusview.Active(statusview.ActiveInput{
			Batch: batch.Batch{ID: "b", Title: "T", PlanIDs: []string{"p"},
				Phases: []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"p"}}}},
			Run:   batch.Run{ActiveBatchID: "b"},
			State: &conductor.State{Plans: map[string]*conductor.PlanState{"p": {Status: conductor.StatusRunning}}},
			Units: []conductor.PlanUnit{{ID: "p", Title: "P"}},
			Live:  live,
		})
	}
	if got := mk(true).Plans[0].Status; got != statusview.StatusRunning {
		t.Errorf("live=true: status = %q, want running", got)
	}
	if got := mk(false).Plans[0].Status; got != statusview.StatusStalled {
		t.Errorf("live=false: status = %q, want stalled", got)
	}
}

func TestDeriveIntegration(t *testing.T) {
	cases := []struct {
		name       string
		ps         *conductor.PlanState
		wantState  string
		wantReason string // "" means reason must be nil
	}{
		{"nil->clean", nil, "clean", ""},
		{"no-merge->clean", &conductor.PlanState{Status: conductor.StatusRunning}, "clean", ""},
		{"merged-clean", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeSucceeded}, Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded}}, "clean", ""},
		{"merge-pending->clean", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergePending}}, "clean", ""},
		{"merge-refused->clean", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeRefused}}, "clean", ""},
		// The masked-attention cases: merge succeeded but NOT integrated.
		{"succeeded-cleanup-failed->needs_attention", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeSucceeded}, Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupFailed}}, "needs_attention", "cleanup-failed"},
		{"succeeded-cleanup-nil->needs_attention", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeSucceeded}}, "needs_attention", "cleanup-failed"},
		{"succeeded-source-sync-failed->needs_attention", &conductor.PlanState{Status: conductor.StatusCompleted, Merge: &conductor.MergeOutcome{Status: conductor.MergeSucceeded, SourceSyncStatus: "failed"}, Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded}}, "needs_attention", "source-sync-failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			iv := statusview.DeriveIntegrationForTest(tc.ps)
			if iv.State != tc.wantState {
				t.Fatalf("state = %q, want %q", iv.State, tc.wantState)
			}
			if tc.wantReason == "" {
				if iv.Reason != nil {
					t.Fatalf("reason = %q, want nil", *iv.Reason)
				}
			} else {
				if iv.Reason == nil || *iv.Reason != tc.wantReason {
					t.Fatalf("reason = %v, want %q", iv.Reason, tc.wantReason)
				}
			}
		})
	}
}

// TestActive_NilStateOmitsProgress verifies that when State is nil (conductor
// state unavailable), the JSON output omits progress and spend (matching the
// text path which suppresses both when state is nil), while still rendering
// batch and plans (built from PlanIDs with nil ps → pending).
func TestActive_NilStateOmitsProgress(t *testing.T) {
	b := batch.Batch{
		ID:      "batch-nil",
		Title:   "Nil State Batch",
		PlanIDs: []string{"p1", "p2"},
		Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"p1", "p2"}}},
	}
	in := statusview.ActiveInput{
		Batch:     b,
		Run:       batch.Run{ActiveBatchID: "batch-nil"},
		State:     nil,
		Units:     []conductor.PlanUnit{{ID: "p1", Title: "Plan 1"}, {ID: "p2", Title: "Plan 2"}},
		HasRollup: false,
	}
	v := statusview.Active(in)

	if v.Progress != nil {
		t.Fatalf("progress must be nil when state is nil; got %+v", v.Progress)
	}
	if v.Spend != nil {
		t.Fatalf("spend must be nil when state is nil; got %+v", v.Spend)
	}
	if v.Batch == nil || v.Batch.ID != "batch-nil" {
		t.Fatalf("batch must still render; got %+v", v.Batch)
	}
	if len(v.Plans) != 2 {
		t.Fatalf("plans must still render from PlanIDs; got %d plans", len(v.Plans))
	}
	for _, pv := range v.Plans {
		if pv.Status != statusview.StatusPending {
			t.Errorf("plan %s: status = %q, want pending (nil ps)", pv.ID, pv.Status)
		}
	}
}

// TestActive_BaseBranch_NonEmpty asserts that a plan with a real BaseRef
// surfaces the value in the JSON (not just the zero-value path in TestActive_JSONShape).
// TestActive_LastRetry_Populated verifies that flags.last_retry is populated
// for active batches when run.LastRetry is non-empty, matching the text path.
func TestActive_LastRetry_Populated(t *testing.T) {
	in := statusview.ActiveInput{
		Batch: batch.Batch{ID: "b", Title: "T", PlanIDs: []string{"p"}},
		Run:   batch.Run{ActiveBatchID: "b", LastRetry: []string{"retry A", "retry B"}},
		State: &conductor.State{Plans: map[string]*conductor.PlanState{"p": {Status: conductor.StatusRunning}}},
		Units: []conductor.PlanUnit{{ID: "p", Title: "Plan P"}},
	}
	v := statusview.Active(in)
	if v.Flags == nil {
		t.Fatal("flags must be non-nil for active batch")
	}
	if len(v.Flags.LastRetry) != 2 {
		t.Fatalf("last_retry = %v, want [retry A retry B]", v.Flags.LastRetry)
	}
	if v.Flags.LastRetry[0] != "retry A" || v.Flags.LastRetry[1] != "retry B" {
		t.Fatalf("last_retry = %v, want [retry A retry B]", v.Flags.LastRetry)
	}
	// Verify omitempty: marshal must include last_retry when non-empty.
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"last_retry"`) {
		t.Errorf("JSON missing last_retry; got %s", b)
	}
}

// TestActive_LastRetry_OmittedWhenEmpty verifies that flags.last_retry is
// omitted (omitempty) when run.LastRetry is empty — no noise for the common case.
func TestActive_LastRetry_OmittedWhenEmpty(t *testing.T) {
	in := statusview.ActiveInput{
		Batch: batch.Batch{ID: "b", Title: "T", PlanIDs: []string{"p"}},
		Run:   batch.Run{ActiveBatchID: "b", LastRetry: nil},
		State: &conductor.State{Plans: map[string]*conductor.PlanState{"p": {Status: conductor.StatusRunning}}},
		Units: []conductor.PlanUnit{{ID: "p", Title: "Plan P"}},
	}
	v := statusview.Active(in)
	if v.Flags == nil {
		t.Fatal("flags must be non-nil for active batch")
	}
	if v.Flags.LastRetry != nil {
		t.Fatalf("last_retry must be nil when run.LastRetry is empty; got %v", v.Flags.LastRetry)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"last_retry"`) {
		t.Errorf("JSON must omit last_retry when empty; got %s", b)
	}
}

// TestOrphan_LastRetry_Omitted confirms that the orphan flags builder does NOT
// populate last_retry (orphan has no active run context).
func TestOrphan_LastRetry_Omitted(t *testing.T) {
	run := batch.Run{ActiveBatchID: "batch-x", LastRetry: []string{"retry A"}}
	v := statusview.Orphan(run)
	if v.Flags == nil {
		t.Fatal("orphan flags must be non-nil")
	}
	if v.Flags.LastRetry != nil {
		t.Fatalf("orphan flags.last_retry must be nil; got %v", v.Flags.LastRetry)
	}
}

func TestActive_BaseBranch_NonEmpty(t *testing.T) {
	in := statusview.ActiveInput{
		Batch: batch.Batch{ID: "b2", Title: "T2", PlanIDs: []string{"p2"}},
		Run:   batch.Run{ActiveBatchID: "b2"},
		State: &conductor.State{Plans: map[string]*conductor.PlanState{
			"p2": {Status: conductor.StatusRunning, BaseRef: "springfield/batch-001"},
		}},
		Units: []conductor.PlanUnit{{ID: "p2", Title: "Plan two"}},
	}
	out, err := json.MarshalIndent(statusview.Active(in), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"base_branch": "springfield/batch-001"`) {
		t.Errorf("active JSON must carry non-empty base_branch; got:\n%s", out)
	}
}

// TestActive_ParallelInFlight asserts that progress.parallel_in_flight reflects
// the computed value from batch.Progress.ParallelInFlight.
func TestActive_ParallelInFlight(t *testing.T) {
	t.Run("parallel_false_for_serial", func(t *testing.T) {
		b := batch.Batch{
			ID:      "b",
			Title:   "T",
			PlanIDs: []string{"p1"},
			Phases:  []batch.Phase{{Mode: batch.PhaseSerial, Plans: []string{"p1"}}},
		}
		state := &conductor.State{Plans: map[string]*conductor.PlanState{
			"p1": {Status: conductor.StatusRunning},
		}}
		in := statusview.ActiveInput{
			Batch: b,
			Run:   batch.Run{ActiveBatchID: "b"},
			State: state,
			Units: []conductor.PlanUnit{{ID: "p1", Title: "Plan 1"}},
		}
		v := statusview.Active(in)
		if v.Progress == nil {
			t.Fatal("progress must be non-nil when state is present")
		}
		if v.Progress.ParallelInFlight {
			t.Error("ParallelInFlight should be false for serial phase")
		}
	})

	t.Run("parallel_true_for_multi_in_flight", func(t *testing.T) {
		b := batch.Batch{
			ID:      "b",
			Title:   "T",
			PlanIDs: []string{"p1", "p2"},
			Phases:  []batch.Phase{{Mode: batch.PhaseParallel, Plans: []string{"p1", "p2"}}},
		}
		state := &conductor.State{Plans: map[string]*conductor.PlanState{
			"p1": {Status: conductor.StatusRunning},
			"p2": {Status: conductor.StatusRunning},
		}}
		in := statusview.ActiveInput{
			Batch: b,
			Run:   batch.Run{ActiveBatchID: "b"},
			State: state,
			Units: []conductor.PlanUnit{{ID: "p1", Title: "Plan 1"}, {ID: "p2", Title: "Plan 2"}},
		}
		v := statusview.Active(in)
		if v.Progress == nil {
			t.Fatal("progress must be non-nil when state is present")
		}
		if !v.Progress.ParallelInFlight {
			t.Error("ParallelInFlight should be true for 2+ in-flight parallel phase")
		}
	})

	t.Run("parallel_false_when_no_state", func(t *testing.T) {
		b := batch.Batch{
			ID:      "b",
			Title:   "T",
			PlanIDs: []string{"p1"},
			Phases:  []batch.Phase{{Mode: batch.PhaseParallel, Plans: []string{"p1"}}},
		}
		in := statusview.ActiveInput{
			Batch: b,
			Run:   batch.Run{ActiveBatchID: "b"},
			State: nil,
			Units: []conductor.PlanUnit{{ID: "p1", Title: "Plan 1"}},
		}
		v := statusview.Active(in)
		if v.Progress != nil {
			t.Fatal("progress must be nil when state is nil")
		}
	})
}
