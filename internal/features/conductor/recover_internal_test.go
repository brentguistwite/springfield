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
