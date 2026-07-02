package planmerge_test

import (
	"errors"
	"os"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planmerge"
)

func TestRetainWorktreeAlreadyGoneIsSuccess(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "develop", "AAAA", "BBBB")
	// Simulate a prior Retain that removed the worktree but failed to persist:
	// the path is gone, but state still says Completed with no Cleanup.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatalf("rm worktree: %v", err)
	}
	g := newFakeGit()

	res := planmerge.Retain(planmerge.RetainInput{Project: project, PlanID: "alpha", ControlRoot: root, Git: g})
	if res.Err != nil {
		t.Fatalf("Retain: %v", res.Err)
	}
	// Must NOT re-attempt removal on an already-absent worktree (that errors
	// and would deadlock the batch re-entry).
	if len(g.worktreeRemoveAll) != 0 {
		t.Fatalf("retain must skip removal of an absent worktree, got %v", g.worktreeRemoveAll)
	}
	if res.Cleanup == nil || res.Cleanup.ExecutionWorktree == nil ||
		res.Cleanup.ExecutionWorktree.Status != conductor.CleanupSucceeded {
		t.Fatalf("absent worktree must record succeeded, got %+v", res.Cleanup)
	}
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.State.Plans["alpha"].IsIntegrated() {
		t.Fatal("idempotent retain on an absent worktree must integrate the plan")
	}
}

func TestRetainKeepsBranchRemovesWorktreeAndIsIntegrated(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "develop", "AAAA", "BBBB")
	g := newFakeGit()

	res := planmerge.Retain(planmerge.RetainInput{
		Project:     project,
		PlanID:      "alpha",
		ControlRoot: root,
		Git:         g,
	})

	if res.Err != nil {
		t.Fatalf("Retain returned err: %v", res.Err)
	}
	// Execution worktree removed directly...
	if len(g.worktreeRemoveAll) != 1 || g.worktreeRemoveAll[0] != wt {
		t.Fatalf("expected execution worktree %q removed once, got %v", wt, g.worktreeRemoveAll)
	}
	// ...but the plan branch is NEVER deleted (the whole point of per-plan mode).
	if len(g.branchDeleteCalls) != 0 {
		t.Fatalf("standalone retain must not delete the plan branch, got %v", g.branchDeleteCalls)
	}
	if res.Merge == nil || res.Merge.Status != conductor.MergeSucceeded {
		t.Fatalf("Merge must be recorded succeeded, got %+v", res.Merge)
	}
	if res.Merge.Mode != "standalone" {
		t.Fatalf("Merge.Mode = %q, want standalone", res.Merge.Mode)
	}
	if res.Cleanup == nil ||
		res.Cleanup.ExecutionWorktree == nil || res.Cleanup.ExecutionWorktree.Status != conductor.CleanupSucceeded ||
		res.Cleanup.PlanBranch == nil || res.Cleanup.PlanBranch.Status != conductor.CleanupPreserved ||
		res.Cleanup.PlanBranch.Branch != "springfield/alpha" {
		t.Fatalf("cleanup ledger wrong: %+v", res.Cleanup)
	}

	// Persisted + queue-advanceable.
	reloaded, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	st := reloaded.State.Plans["alpha"]
	if !st.IsIntegrated() {
		t.Fatalf("standalone-retained plan must count as integrated: %+v", st)
	}
}

func TestRetainWorktreeRemoveFailurePreservesBranch(t *testing.T) {
	root, project, wt := projectFixture(t, "alpha", "springfield/alpha", "develop", "AAAA", "BBBB")
	g := newFakeGit()
	g.worktreeRemoveErr[wt] = errors.New("device busy")

	res := planmerge.Retain(planmerge.RetainInput{
		Project:     project,
		PlanID:      "alpha",
		ControlRoot: root,
		Git:         g,
	})

	if res.Cleanup == nil || res.Cleanup.ExecutionWorktree == nil ||
		res.Cleanup.ExecutionWorktree.Status != conductor.CleanupFailed {
		t.Fatalf("worktree-remove failure must record execution worktree failed: %+v", res.Cleanup)
	}
	if res.Cleanup.Status != conductor.CleanupFailed {
		t.Fatalf("aggregate cleanup must be failed, got %q", res.Cleanup.Status)
	}
	// Branch is preserved regardless of worktree-remove failure.
	if res.Cleanup.PlanBranch == nil || res.Cleanup.PlanBranch.Status != conductor.CleanupPreserved {
		t.Fatalf("plan branch must stay preserved on worktree-remove failure: %+v", res.Cleanup.PlanBranch)
	}
	if len(g.branchDeleteCalls) != 0 {
		t.Fatalf("branch must never be deleted, got %v", g.branchDeleteCalls)
	}
	// A failed cleanup is not queue-integrated — operator inspects.
	reloaded, _ := conductor.LoadProject(root)
	if reloaded.State.Plans["alpha"].IsIntegrated() {
		t.Fatalf("cleanup-failed plan must not count as integrated")
	}
	_ = wt
}

func TestRetainRefusesNonCompletedPlan(t *testing.T) {
	root, project, _ := projectFixture(t, "alpha", "springfield/alpha", "develop", "AAAA", "BBBB")
	project.State.Plans["alpha"].Status = conductor.StatusRunning
	if err := project.SaveState(); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	g := newFakeGit()

	res := planmerge.Retain(planmerge.RetainInput{Project: project, PlanID: "alpha", ControlRoot: root, Git: g})
	if res.Err == nil {
		t.Fatal("retain must refuse a non-completed plan rather than stamp a fraudulent success")
	}
	if len(g.worktreeRemoveAll) != 0 {
		t.Fatalf("retain must not remove the worktree on refusal, got %v", g.worktreeRemoveAll)
	}
}
