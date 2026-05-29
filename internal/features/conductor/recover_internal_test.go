package conductor

import "testing"

func newProjectWithPlan(id string, status PlanStatus) *Project {
	return &Project{
		Config: &Config{},
		State:  &State{Plans: map[string]*PlanState{id: {Status: status, Error: "halted", Merge: &MergeOutcome{Status: MergePending}}}},
	}
}

func TestRecoverRetryAcceptsNeedsHuman(t *testing.T) {
	p := newProjectWithPlan("P", StatusNeedsHuman)
	rec, err := p.RecoverRetry("P")
	if err != nil {
		t.Fatalf("RecoverRetry on needs-human: unexpected error %v", err)
	}
	if rec == nil || rec.Action != "retry" {
		t.Fatalf("want a retry recovery action, got %+v", rec)
	}
	got := p.State.Plans["P"]
	if got.Status != StatusPending {
		t.Fatalf("status = %q, want pending after retry", got.Status)
	}
	if got.Error != "" || got.Merge != nil {
		t.Fatalf("retry must clear Error and Merge, got Error=%q Merge=%+v", got.Error, got.Merge)
	}
}

func TestRecoverRetryStillRejectsCompleted(t *testing.T) {
	p := newProjectWithPlan("P", StatusCompleted)
	if _, err := p.RecoverRetry("P"); err == nil {
		t.Fatal("RecoverRetry on completed should still error")
	}
}

func TestAcceptInputDriftRecordsDigestAndResets(t *testing.T) {
	p := newProjectWithPlan("P", StatusFailed)
	ps := p.State.Plans["P"]
	ps.InputDigest = "sha256:stale"
	ps.ExitReason = "preflight-input-drift"
	ps.Cleanup = &CleanupOutcome{Status: CleanupPreserved}

	rec, err := p.AcceptInputDrift("P", "sha256:fresh")
	if err != nil {
		t.Fatalf("AcceptInputDrift: unexpected error %v", err)
	}
	if rec == nil || rec.Action != "accept-drift" {
		t.Fatalf("want an accept-drift recovery action, got %+v", rec)
	}

	got := p.State.Plans["P"]
	if got.InputDigest != "sha256:fresh" {
		t.Fatalf("InputDigest = %q, want sha256:fresh", got.InputDigest)
	}
	if got.Status != StatusPending {
		t.Fatalf("status = %q, want pending after accept-drift", got.Status)
	}
	if got.Error != "" || got.ExitReason != "" || got.Merge != nil || got.Cleanup != nil {
		t.Fatalf("accept-drift must clear failure state, got Error=%q ExitReason=%q Merge=%+v Cleanup=%+v",
			got.Error, got.ExitReason, got.Merge, got.Cleanup)
	}
	if len(got.RecoveryHistory) != 1 {
		t.Fatalf("expected one recovery-history entry, got %+v", got.RecoveryHistory)
	}
}

func TestAcceptInputDriftRejectsCompleted(t *testing.T) {
	p := newProjectWithPlan("P", StatusCompleted)
	if _, err := p.AcceptInputDrift("P", "sha256:fresh"); err == nil {
		t.Fatal("AcceptInputDrift on completed should error (use retry-merge/retry-integration)")
	}
}

func TestAcceptInputDriftRejectsUnknownPlan(t *testing.T) {
	p := newProjectWithPlan("P", StatusFailed)
	if _, err := p.AcceptInputDrift("missing", "sha256:fresh"); err == nil {
		t.Fatal("AcceptInputDrift on an unregistered plan should error")
	}
}

// TestAcceptInputDriftRejectsRunning pins the StatusRunning guard: a running
// plan committed to the preflight-time digest, and rewriting InputDigest while
// the runner is mid-flight leaves the recorded value out of sync with what the
// runner is actually executing against.
func TestAcceptInputDriftRejectsRunning(t *testing.T) {
	p := newProjectWithPlan("P", StatusRunning)
	if _, err := p.AcceptInputDrift("P", "sha256:fresh"); err == nil {
		t.Fatal("AcceptInputDrift on a running plan should error (would race the live runner)")
	}
}

// TestAcceptInputDriftIdempotentOnMatchingDigest pins the no-op guard: a
// pending plan whose recorded digest already matches the supplied digest has
// no drift to accept. Appending a "accepted drift" history entry for a no-op
// would silently misrepresent the recovery log.
func TestAcceptInputDriftIdempotentOnMatchingDigest(t *testing.T) {
	p := newProjectWithPlan("P", StatusPending)
	p.State.Plans["P"].InputDigest = "sha256:same"
	if _, err := p.AcceptInputDrift("P", "sha256:same"); err == nil {
		t.Fatal("AcceptInputDrift on pending+matching-digest should error as a no-op")
	}
	if got := p.State.Plans["P"]; len(got.RecoveryHistory) != 0 {
		t.Fatalf("no-op accept-drift must NOT append history, got %+v", got.RecoveryHistory)
	}
}

// TestMarkPlanCompletedRejectsRunning pins the StatusRunning guard for the A9
// path: a live runner's next SaveState would silently overwrite the operator's
// flip to StatusCompleted.
func TestMarkPlanCompletedRejectsRunning(t *testing.T) {
	p := newProjectWithPlan("P", StatusRunning)
	if _, err := p.MarkPlanCompleted("P", nil); err == nil {
		t.Fatal("MarkPlanCompleted on a running plan should error (would race the live runner)")
	}
}
