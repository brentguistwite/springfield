package planrun_test

import (
	"errors"
	"testing"
	"time"

	"springfield/internal/core/agents"
	coreexec "springfield/internal/core/exec"
	coreruntime "springfield/internal/core/runtime"
	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/prd"
)

// TestSinglePlanCompletesWhenWorkDoneThenCrash pins A7: an agent that emits
// every story-pass + COMPLETE and then exits non-zero (post-completion crash)
// must be judged COMPLETED, not failed. The crash is teardown noise once the
// work is provably done — the markers landed, prd.json is all-passed, and the
// worktree advanced beyond base.
func TestSinglePlanCompletesWhenWorkDoneThenCrash(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit() // Head() ≠ base head, so the worktree-advanced check holds.
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusFailed,
				ExitCode: 1,
				Err:      errors.New("claude API 400 after completion"),
				Events: []coreexec.Event{
					{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass><promise>COMPLETE</promise>", Time: time.Now()},
				},
				StartedAt: time.Now().Add(-time.Second),
				EndedAt:   time.Now(),
			},
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
		t.Fatalf("post-completion crash must not surface as failure, got: %v", res.Err)
	}
	if res.Status != conductor.StatusCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}

	// Persisted state must record completed + MergePending so the batch loop
	// proceeds to merge integration.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["feat"]
	if st == nil {
		t.Fatal("expected persisted state for feat")
	}
	if st.Status != conductor.StatusCompleted {
		t.Fatalf("persisted status = %s, want completed", st.Status)
	}
	if st.Merge == nil || st.Merge.Status != conductor.MergePending {
		t.Fatalf("expected MergePending, got %+v", st.Merge)
	}
	if st.Error != "" {
		t.Fatalf("completed plan must not persist an error, got %q", st.Error)
	}
}
