package planrun_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/core/agents"
	"springfield/internal/core/config"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/notify"
	"springfield/internal/features/prd"
)

// spyNotifier records every Event without touching the OS, so a stall escalation
// can be asserted without spawning osascript or a shell (mirrors cmd/start's
// fakeNotifier).
type spyNotifier struct {
	events []notify.Event
}

func (s *spyNotifier) Notify(e notify.Event) { s.events = append(s.events, e) }

// stallFiringRunner simulates the runtime classifying a dispatch as
// possibly-wedged: it fires the OnStall callback SinglePlan wired into the
// Request (twice, to model a plan that recovered and re-wedged) BEFORE returning
// a clean pass+complete, then snapshots the plan's live PlanStall so the test can
// assert the status-visible signal that a terminal transition later clears.
type stallFiringRunner struct {
	project    *conductor.Project
	planID     string
	fireCount  int
	sawThresh  time.Duration
	liveStall  *conductor.PlanStall
	sawOnStall bool
}

func (r *stallFiringRunner) Run(_ context.Context, req coreruntime.Request) coreruntime.Result {
	r.sawThresh = req.StallThreshold
	if req.OnStall != nil {
		r.sawOnStall = true
		for i := 0; i < r.fireCount; i++ {
			req.OnStall()
		}
	}
	// Snapshot the live signal mid-dispatch (before the terminal transition that
	// replaces PlanState wholesale).
	if ps, ok := r.project.ReadPlan(r.planID); ok && ps.Stall != nil {
		s := *ps.Stall
		r.liveStall = &s
	}
	return coreruntime.Result{
		Agent:    agents.AgentClaude,
		Status:   coreruntime.StatusPassed,
		ExitCode: 0,
		Events: []coreexec.Event{
			{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>", Time: time.Now()},
		},
		StartedAt: time.Now().Add(-time.Second),
		EndedAt:   time.Now(),
	}
}

// TestSinglePlanEscalatesWedgeOnEverySurface pins US-002's escalation contract:
// when a dispatch is classified possibly-wedged, SinglePlan (1) surfaces the
// signal on the live PlanState so `springfield status` shows it, (2) records each
// occurrence in the plan's evidence for post-hoc diagnosis, and (3) emits a
// notify.Stalled Event naming the plan and staleness through the Notifier seam —
// all without ever failing or interrupting the plan (it still completes).
func TestSinglePlanEscalatesWedgeOnEverySurface(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}
	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()
	notifier := &spyNotifier{}
	runner := &stallFiringRunner{project: project, planID: "feat", fireCount: 2}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
		StallConfig:  config.StallConfig{Threshold: "30s"},
		Notifier:     notifier,
		BatchID:      "batch-9",
	})

	if res.Err != nil {
		t.Fatalf("SinglePlan errored: %v", res.Err)
	}
	// Advisory-only: a wedge NEVER fails the plan; it still completes.
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed (a wedge must never fail the plan)", res.Status)
	}

	// The dispatch must have received the wired threshold + OnStall seam.
	if !runner.sawOnStall {
		t.Fatal("SinglePlan did not wire OnStall into the dispatch Request")
	}
	if runner.sawThresh != 30*time.Second {
		t.Fatalf("dispatch StallThreshold = %s, want 30s", runner.sawThresh)
	}

	// (1) Live status signal: two idle stretches → Occurrences 2, threshold named.
	if runner.liveStall == nil {
		t.Fatal("PlanState.Stall was not set mid-dispatch (status could not surface the wedge)")
	}
	if runner.liveStall.Occurrences != 2 {
		t.Fatalf("PlanStall.Occurrences = %d, want 2 (recurring wedge)", runner.liveStall.Occurrences)
	}
	if runner.liveStall.StaleFor != "30s" {
		t.Fatalf("PlanStall.StaleFor = %q, want 30s", runner.liveStall.StaleFor)
	}

	// (2) Evidence: one JSONL record per occurrence, in the plan's evidence dir.
	stallsPath := filepath.Join(planrun.EvidenceRoot(root, "feat"), "stalls.jsonl")
	data, err := os.ReadFile(stallsPath)
	if err != nil {
		t.Fatalf("read stalls.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("stall records = %d, want 2", len(lines))
	}
	var rec struct {
		PlanID   string `json:"plan_id"`
		StaleFor string `json:"stale_for"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("unmarshal stall record: %v", err)
	}
	if rec.PlanID != "feat" || rec.StaleFor != "30s" {
		t.Fatalf("stall record = %+v, want plan feat / stale_for 30s", rec)
	}

	// (3) Notifier seam: one Stalled Event per occurrence, naming plan + staleness.
	var stalled []notify.Event
	for _, e := range notifier.events {
		if e.Kind == notify.Stalled {
			stalled = append(stalled, e)
		}
	}
	if len(stalled) != 2 {
		t.Fatalf("Stalled notifications = %d, want 2", len(stalled))
	}
	if stalled[0].PlanID != "feat" || stalled[0].Detail != "30s" || stalled[0].BatchID != "batch-9" {
		t.Fatalf("Stalled event = %+v, want plan feat / detail 30s / batch batch-9", stalled[0])
	}
}
