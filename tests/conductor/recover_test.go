package conductor_test

import (
	"testing"
	"time"

	"springfield/internal/features/conductor"
)

func TestRecoverRetryFailedPlan(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha", "beta"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "/evidence/alpha")

	rec, err := project.RecoverRetry("alpha")
	if err != nil {
		t.Fatalf("recover retry: %v", err)
	}

	if rec.Action != "retry" {
		t.Fatalf("action = %q", rec.Action)
	}
	if rec.At.IsZero() {
		t.Fatal("recorded timestamp is zero")
	}

	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusPending {
		t.Fatalf("status = %q, want pending", ps.Status)
	}
	if ps.Error != "" {
		t.Fatalf("error should be cleared, got %q", ps.Error)
	}
	if ps.ExitReason != "" {
		t.Fatalf("exit_reason should be cleared, got %q", ps.ExitReason)
	}
	if ps.Merge != nil {
		t.Fatal("merge should be nil")
	}
	if ps.Cleanup != nil {
		t.Fatal("cleanup should be nil")
	}
	if len(ps.RecoveryHistory) != 1 {
		t.Fatalf("recovery history = %d, want 1", len(ps.RecoveryHistory))
	}
}

func TestRecoverRetryInterruptedPlan(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status:       conductor.StatusInterrupted,
		Error:        "process exited",
		ExitReason:   conductor.ExitInterruptedProcessExit,
		Attempts:     1,
		WorktreePath: root + "/.worktrees/alpha",
	}

	rec, err := project.RecoverRetry("alpha")
	if err != nil {
		t.Fatalf("recover retry: %v", err)
	}
	if rec.Action != "retry" {
		t.Fatalf("action = %q", rec.Action)
	}

	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusPending {
		t.Fatalf("status = %q", ps.Status)
	}
	if ps.Attempts != 1 {
		t.Fatalf("attempts should be preserved = %d", ps.Attempts)
	}
}

func TestRecoverRetryRejectsCompletedPlan(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkCompleted("alpha", "claude", "")

	_, err = project.RecoverRetry("alpha")
	if err == nil {
		t.Fatal("expected error for completed plan")
	}
}

func TestRecoverRetryRejectsPendingPlan(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	_, err = project.RecoverRetry("alpha")
	if err == nil {
		t.Fatal("expected error for pending plan (no state)")
	}
}

func TestRecoverRetryRejectsUnknownPlan(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	_, err = project.RecoverRetry("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown plan")
	}
}

func TestRecoverRetryMergeRefused(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status: conductor.MergeRefused,
			Reason: "target-drift",
		},
		Cleanup: &conductor.CleanupOutcome{
			Status: conductor.CleanupPreserved,
		},
	}

	rec, err := project.RecoverRetryMerge("alpha")
	if err != nil {
		t.Fatalf("recover retry-merge: %v", err)
	}
	if rec.Action != "retry-merge" {
		t.Fatalf("action = %q", rec.Action)
	}

	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusCompleted {
		t.Fatalf("status should remain completed, got %q", ps.Status)
	}
	if ps.Merge != nil {
		t.Fatal("merge should be cleared")
	}
	if ps.Cleanup != nil {
		t.Fatal("cleanup should be cleared")
	}
	if len(ps.RecoveryHistory) != 1 {
		t.Fatalf("recovery history = %d", len(ps.RecoveryHistory))
	}
}

func TestRecoverRetryMergeFailed(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status: conductor.MergeFailed,
			Reason: "ref-update-failed",
			Error:  "CAS lost",
		},
		Cleanup: &conductor.CleanupOutcome{
			Status: conductor.CleanupPreserved,
		},
	}

	rec, err := project.RecoverRetryMerge("alpha")
	if err != nil {
		t.Fatalf("recover retry-merge: %v", err)
	}
	if rec.Action != "retry-merge" {
		t.Fatalf("action = %q", rec.Action)
	}

	ps := project.State.Plans["alpha"]
	if ps.Merge != nil {
		t.Fatal("merge should be cleared for pre-publish failure")
	}
	if ps.Cleanup != nil {
		t.Fatal("cleanup should be cleared")
	}
}

func TestRecoverRetryMergeRejectsMergeSucceeded(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status: conductor.MergeSucceeded,
		},
		Cleanup: &conductor.CleanupOutcome{
			Status: conductor.CleanupFailed,
		},
	}

	_, err = project.RecoverRetryMerge("alpha")
	if err == nil {
		t.Fatal("retry-merge should reject merge-succeeded plans")
	}
}

func TestRecoverRetryIntegrationCleanupFailed(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status:        conductor.MergeSucceeded,
			PostMergeHead: "aabbccdd",
			TargetRef:     "main",
			WorktreePath:  root + "/.merges/alpha",
		},
		Cleanup: &conductor.CleanupOutcome{
			Status: conductor.CleanupFailed,
		},
	}

	rec, err := project.RecoverRetryIntegration("alpha")
	if err != nil {
		t.Fatalf("recover retry-integration: %v", err)
	}
	if rec.Action != "retry-integration" {
		t.Fatalf("action = %q", rec.Action)
	}

	ps := project.State.Plans["alpha"]
	if ps.Merge == nil {
		t.Fatal("merge should be preserved")
	}
	if ps.Merge.Status != conductor.MergeSucceeded {
		t.Fatalf("merge status = %q, want succeeded", ps.Merge.Status)
	}
	if ps.Merge.PostMergeHead != "aabbccdd" {
		t.Fatalf("PostMergeHead = %q, want aabbccdd", ps.Merge.PostMergeHead)
	}
	if ps.Merge.TargetRef != "main" {
		t.Fatalf("TargetRef = %q, want main", ps.Merge.TargetRef)
	}
	if ps.Cleanup != nil {
		t.Fatal("cleanup should be cleared")
	}
	if len(ps.RecoveryHistory) != 1 {
		t.Fatalf("recovery history = %d", len(ps.RecoveryHistory))
	}
}

func TestRecoverRetryIntegrationSourceSyncFailed(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status:           conductor.MergeSucceeded,
			PostMergeHead:    "eeff0011",
			TargetRef:        "main",
			SourceSyncStatus: "failed",
			SourceSyncError:  "git reset refused",
		},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}

	rec, err := project.RecoverRetryIntegration("alpha")
	if err != nil {
		t.Fatalf("recover retry-integration: %v", err)
	}
	if rec.Action != "retry-integration" {
		t.Fatalf("action = %q", rec.Action)
	}

	ps := project.State.Plans["alpha"]
	if ps.Merge == nil {
		t.Fatal("merge should be preserved")
	}
	if ps.Merge.PostMergeHead != "eeff0011" {
		t.Fatalf("PostMergeHead = %q", ps.Merge.PostMergeHead)
	}
	if ps.Merge.SourceSyncStatus != "" {
		t.Fatalf("SourceSyncStatus should be cleared, got %q", ps.Merge.SourceSyncStatus)
	}
	if ps.Merge.SourceSyncError != "" {
		t.Fatalf("SourceSyncError should be cleared, got %q", ps.Merge.SourceSyncError)
	}
	if ps.Cleanup != nil {
		t.Fatal("cleanup should be cleared")
	}
}

func TestRecoverRetryIntegrationMergeRefused(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status: conductor.MergeRefused,
		},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupPreserved},
	}

	_, err = project.RecoverRetryIntegration("alpha")
	if err == nil {
		t.Fatal("retry-integration should reject non-succeeded merge")
	}
}

func TestRecoverRetryIntegrationAlreadyIntegrated(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status:           conductor.MergeSucceeded,
			SourceSyncStatus: "synced",
		},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}

	_, err = project.RecoverRetryIntegration("alpha")
	if err == nil {
		t.Fatal("retry-integration should reject fully integrated plan")
	}
}

func TestRecoverRetryMergeRejectsNonCompleted(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")

	_, err = project.RecoverRetryMerge("alpha")
	if err == nil {
		t.Fatal("expected error for non-completed plan")
	}
}

func TestRecoverRetryMergeRejectsMergePending(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status: conductor.MergePending,
		},
	}

	_, err = project.RecoverRetryMerge("alpha")
	if err == nil {
		t.Fatal("expected error for merge-pending plan")
	}
}

func TestRecoverRetryMergeRejectsAlreadyIntegrated(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status:           conductor.MergeSucceeded,
			SourceSyncStatus: "synced",
		},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}

	_, err = project.RecoverRetryMerge("alpha")
	if err == nil {
		t.Fatal("expected error for fully integrated plan")
	}
}

func TestRecoverRetryMergeRejectsNoMergeState(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkCompleted("alpha", "claude", "")

	_, err = project.RecoverRetryMerge("alpha")
	if err == nil {
		t.Fatal("expected error for plan with no merge state")
	}
}

func TestRecoveryHistoryAppends(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	// First failure + recovery
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")
	if _, err := project.RecoverRetry("alpha"); err != nil {
		t.Fatalf("first recovery: %v", err)
	}

	// Second failure + recovery
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 2", "codex", "")
	if _, err := project.RecoverRetry("alpha"); err != nil {
		t.Fatalf("second recovery: %v", err)
	}

	ps := project.State.Plans["alpha"]
	if len(ps.RecoveryHistory) != 2 {
		t.Fatalf("recovery history = %d, want 2", len(ps.RecoveryHistory))
	}
	if ps.RecoveryHistory[0].Action != "retry" || ps.RecoveryHistory[1].Action != "retry" {
		t.Fatalf("recovery actions = %+v", ps.RecoveryHistory)
	}
}

func TestRecoveryHistoryPersistsThroughSaveLoad(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")
	if _, err := project.RecoverRetry("alpha"); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if err := project.SaveState(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	// Reload from disk
	project2, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}

	ps := project2.State.Plans["alpha"]
	if ps == nil {
		t.Fatal("plan state lost after reload")
	}
	if len(ps.RecoveryHistory) != 1 {
		t.Fatalf("recovery history lost after reload: got %d", len(ps.RecoveryHistory))
	}
	if ps.RecoveryHistory[0].Action != "retry" {
		t.Fatalf("recovery action = %q after reload", ps.RecoveryHistory[0].Action)
	}
	if ps.RecoveryHistory[0].At.Before(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("recovery timestamp looks wrong: %v", ps.RecoveryHistory[0].At)
	}
}
