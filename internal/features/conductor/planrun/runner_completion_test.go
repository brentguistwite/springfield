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

// TestSinglePlanFailsWhenCompleteButStoryStillPending pins the all-stories-pass
// guard (A7 regression): an agent that emits COMPLETE and passes the current
// story, then exits non-zero, must STILL be judged FAILED while any other story
// remains passes=false. COMPLETE alone — without every story honored — is not a
// post-success crash; it is a genuine mid-work failure. The completeHonored
// computation (complete && NextStory==PickAllPassed) is the guard under test:
// here NextStory still returns the pending US-002, so completeHonored stays
// false and workCompletedBeforeCrash short-circuits to the exit-code path.
func TestSinglePlanFailsWhenCompleteButStoryStillPending(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
			{ID: "US-002", Title: "Story 2", Priority: 2, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit() // worktree advanced; irrelevant — completeHonored gates first.

	// Iteration 1 targets US-001. Agent passes US-001 AND emits COMPLETE
	// prematurely (US-002 still pending), then exits non-zero.
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusFailed,
				ExitCode: 1,
				Err:      errors.New("crash after premature COMPLETE"),
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

	if res.Err == nil {
		t.Fatal("expected failure: COMPLETE with a story still pending is not a post-success crash")
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("Status = %s, want failed (all-stories-pass guard must catch premature COMPLETE)", res.Status)
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["feat"]
	if st == nil {
		t.Fatal("expected persisted state for feat")
	}
	if st.Status != conductor.StatusFailed {
		t.Fatalf("persisted Status = %s, want failed", st.Status)
	}
	if st.ExitReason != "agent-failed" {
		t.Fatalf("ExitReason = %q, want agent-failed", st.ExitReason)
	}
	if st.Merge != nil {
		t.Fatalf("failed plan must not be marked MergePending, got %+v", st.Merge)
	}
}

// TestSinglePlanFailsWhenStoriesPassButNoComplete pins the COMPLETE guard (A7
// regression): an agent that passes the (only) story but NEVER emits
// <promise>COMPLETE</promise>, then exits non-zero, must be judged FAILED.
// passes==true across all stories is necessary but not sufficient — without
// Springfield's own honored COMPLETE record, the crash is treated as genuine
// mid-work failure. Here complete==false, so completeHonored stays false and
// workCompletedBeforeCrash short-circuits to the exit-code path.
func TestSinglePlanFailsWhenStoriesPassButNoComplete(t *testing.T) {
	p := prd.PRD{
		ID:    "feat",
		Title: "Feature Plan",
		UserStories: []prd.UserStory{
			{ID: "US-001", Title: "Story 1", Priority: 1, Passes: false},
		},
	}

	root, project := projectFixtureWithPRD(t, "feat", p)
	g := newFakeGit() // worktree advanced; irrelevant — completeHonored gates first.

	// Agent passes US-001 (all stories now pass) but emits NO COMPLETE marker,
	// then exits non-zero.
	runner := &iterScriptRunner{
		replies: []coreruntime.Result{
			{
				Agent:    agents.AgentClaude,
				Status:   coreruntime.StatusFailed,
				ExitCode: 1,
				Err:      errors.New("crash without COMPLETE"),
				Events: []coreexec.Event{
					{Type: coreexec.EventStdout, Data: "<story-pass>US-001</story-pass>", Time: time.Now()},
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

	if res.Err == nil {
		t.Fatal("expected failure: stories passed but no COMPLETE is not a post-success crash")
	}
	if res.Status != conductor.StatusFailed {
		t.Fatalf("Status = %s, want failed (COMPLETE guard must catch missing marker)", res.Status)
	}

	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-LoadProject: %v", err)
	}
	st := reloaded.State.Plans["feat"]
	if st == nil {
		t.Fatal("expected persisted state for feat")
	}
	if st.Status != conductor.StatusFailed {
		t.Fatalf("persisted Status = %s, want failed", st.Status)
	}
	if st.ExitReason != "agent-failed" {
		t.Fatalf("ExitReason = %q, want agent-failed", st.ExitReason)
	}
	if st.Merge != nil {
		t.Fatalf("failed plan must not be marked MergePending, got %+v", st.Merge)
	}
}
