package planrun_test

import (
	"context"
	"testing"

	"springfield/internal/core/agents"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/prd"
)

// activityProbeRunner simulates a concurrent status reader: on every dispatch it
// re-loads the persisted conductor state from disk (exactly what a separate
// `springfield status` process would do) and records the plan's in-flight
// Activity as observed at that moment. This proves the runner stamped Activity
// via enterPhase BEFORE dispatching the agent, and that the stamp is durable.
type activityProbeRunner struct {
	root    string
	planID  string
	replies []coreruntime.Result
	seen    []*conductor.PlanActivity
	calls   int
}

func (r *activityProbeRunner) Run(_ context.Context, _ coreruntime.Request) coreruntime.Result {
	// Read-through: a status reader loads from state.json, not shared memory.
	if proj, err := conductor.LoadProject(r.root); err == nil {
		if ps := proj.State.Plans[r.planID]; ps != nil {
			r.seen = append(r.seen, ps.Activity)
		} else {
			r.seen = append(r.seen, nil)
		}
	} else {
		r.seen = append(r.seen, nil)
	}
	r.calls++
	if r.calls > len(r.replies) {
		return coreruntime.Result{Agent: agents.AgentClaude, Status: coreruntime.StatusFailed, ExitCode: 1}
	}
	return r.replies[r.calls-1]
}

// TestSinglePlanStampsActivityEachIteration is the US-003 acceptance: the story
// loop stamps Activity via enterPhase each iteration, and a concurrent read
// observes phase=implementing with the current story as detail and the
// iteration as the round. The two-story fixture proves the detail tracks the
// per-iteration target (US-001 then US-002), not a stale first value.
func TestSinglePlanStampsActivityEachIteration(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
			{ID: "US-002", Title: "Story 2", Priority: 2, Passes: false},
		},
	}
	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit()
	runner := &activityProbeRunner{
		root:   root,
		planID: "feat",
		replies: []coreruntime.Result{
			makePassResult("US-001"),
			makePassAndCompleteResult("US-002"),
		},
	}

	res := planrun.SinglePlan(planrun.SinglePlanInput{
		Project:      project,
		ControlRoot:  root,
		WorktreeBase: ".worktrees",
		AgentIDs:     []agents.ID{agents.AgentClaude},
		Runner:       runner,
		Manager:      &planrun.Manager{Git: g},
		ProjectRoot:  root,
	})
	if res.Err != nil {
		t.Fatalf("SinglePlan: %v", res.Err)
	}
	if len(runner.seen) != 2 {
		t.Fatalf("expected 2 dispatches, got %d", len(runner.seen))
	}

	want := []struct {
		detail string
		round  int
	}{
		{"US-001", 1},
		{"US-002", 2},
	}
	for i, w := range want {
		act := runner.seen[i]
		if act == nil {
			t.Fatalf("iteration %d: concurrent read observed no Activity; enterPhase did not stamp before dispatch", i+1)
		}
		if act.Phase != conductor.PhaseImplementing {
			t.Errorf("iteration %d: phase = %q, want %q", i+1, act.Phase, conductor.PhaseImplementing)
		}
		if act.Detail != w.detail {
			t.Errorf("iteration %d: detail = %q, want %q", i+1, act.Detail, w.detail)
		}
		if act.Round != w.round {
			t.Errorf("iteration %d: round = %d, want %d", i+1, act.Round, w.round)
		}
		if act.UpdatedAt.IsZero() {
			t.Errorf("iteration %d: UpdatedAt must be stamped, got zero", i+1)
		}
	}
}
