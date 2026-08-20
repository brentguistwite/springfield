package statusview_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/statusview"
)

// TestRenderActivePlansSurfacesActivityAgentElapsed pins that a watch frame
// rendered from a View shows, per running plan, its status, the in-flight
// activity (phase/detail/round), the agent in use, and elapsed time — all from
// the View alone (no second projection).
func TestRenderActivePlansSurfacesActivityAgentElapsed(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	started := now.Add(-3*time.Minute - 12*time.Second)
	v := statusview.View{
		State:   "active",
		Summary: "Batch batch-001: 0/2 plans integrated.",
		Batch:   &statusview.BatchView{ID: "batch-001", Title: "Active Batch"},
		Plans: []statusview.PlanView{
			{
				ID:        "01",
				Status:    statusview.StatusRunning,
				Agent:     "claude",
				StartedAt: &started,
				Activity:  &statusview.ActivityView{Phase: "implementing", Detail: "US-001", Round: 2},
			},
			{ID: "02", Status: statusview.StatusPending},
		},
	}

	out := statusview.Render(v, now)

	for _, want := range []string{
		"Batch batch-001: 0/2 plans integrated.",
		"01", statusview.StatusRunning,
		"implementing US-001 (round 2)",
		"claude",
		"3m12s",
		"02", statusview.StatusPending,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

// TestRenderShowsElapsedForStalledPlan pins that a stalled plan (started, then
// its owning process died) still shows elapsed-since-start — the number that
// tells an operator how long it has been stuck. It carries a StartedAt like a
// running plan, so the row must not swallow the duration just because the plan
// left the running state.
func TestRenderShowsElapsedForStalledPlan(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	started := now.Add(-5 * time.Minute)
	v := statusview.View{
		State:   "active",
		Summary: "Batch batch-001: 0/1 plans integrated.",
		Plans: []statusview.PlanView{
			{ID: "01", Status: statusview.StatusStalled, StartedAt: &started},
		},
	}

	out := statusview.Render(v, now)

	if !strings.Contains(out, statusview.StatusStalled) {
		t.Fatalf("render missing stalled status:\n%s", out)
	}
	if !strings.Contains(out, "5m00s") {
		t.Fatalf("stalled plan must show elapsed-since-start:\n%s", out)
	}
}

// TestRenderOmitsElapsedAndActivityForNonRunning pins the "never lie" contract:
// a non-running plan (nil Activity, no StartedAt) shows no phase and no elapsed
// duration.
func TestRenderOmitsElapsedAndActivityForNonRunning(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	v := statusview.View{
		State:   "active",
		Summary: "Batch batch-001: 1/1 plans integrated.",
		Plans:   []statusview.PlanView{{ID: "01", Status: statusview.StatusPending}},
	}
	out := statusview.Render(v, now)
	if strings.Contains(out, "implementing") {
		t.Fatalf("pending plan must not show a phase:\n%s", out)
	}
	if strings.Contains(out, "s\n") && strings.Contains(out, "0s") {
		t.Fatalf("pending plan must not show elapsed:\n%s", out)
	}
}

// TestPollIdleWhenNoControlPlane pins that Poll degrades to the Idle view (not
// an error) when there is no run cursor, and touches nothing.
func TestPollIdleWhenNoControlPlane(t *testing.T) {
	root := t.TempDir()
	v, err := statusview.Poll(root)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if v.State != "idle" {
		t.Fatalf("state = %q, want idle", v.State)
	}
	if _, statErr := os.Stat(root + "/.springfield"); statErr == nil {
		t.Fatalf("Poll must not create .springfield/")
	}
}
