package conductor_test

import (
	"strings"
	"testing"
	"time"

	"springfield/internal/features/conductor"
)

func TestDiagnosePlanFailedExecution(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha", "beta"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "/evidence/alpha")

	diag := conductor.DiagnosePlan(project, "alpha", nil)

	if diag.Status != conductor.StatusFailed {
		t.Fatalf("status = %q, want failed", diag.Status)
	}
	if diag.Error != "exit code 1" {
		t.Fatalf("error = %q", diag.Error)
	}
	if diag.Agent != "claude" {
		t.Fatalf("agent = %q", diag.Agent)
	}
	if diag.EvidencePath != "/evidence/alpha" {
		t.Fatalf("evidence = %q", diag.EvidencePath)
	}
	if diag.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", diag.Attempts)
	}
	if len(diag.AvailableActions) != 1 || diag.AvailableActions[0].Action != "retry" {
		t.Fatalf("actions = %+v, want [retry]", diag.AvailableActions)
	}
}

func TestDiagnosePlanInterrupted(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status:       conductor.StatusInterrupted,
		Attempts:     1,
		WorktreePath: root + "/.worktrees/alpha",
		Branch:       "springfield/alpha",
		BaseRef:      "main",
		BaseHead:     "aaaa",
		ExitReason:   conductor.ExitInterruptedProcessExit,
	}

	diag := conductor.DiagnosePlan(project, "alpha", nil)

	if diag.Status != conductor.StatusInterrupted {
		t.Fatalf("status = %q", diag.Status)
	}
	if diag.ExitReason != conductor.ExitInterruptedProcessExit {
		t.Fatalf("exit reason = %q", diag.ExitReason)
	}
	if len(diag.AvailableActions) != 1 || diag.AvailableActions[0].Action != "retry" {
		t.Fatalf("actions = %+v", diag.AvailableActions)
	}
}

func TestDiagnosePlanMergeRefused(t *testing.T) {
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
			Status:     conductor.MergeRefused,
			Reason:     "target-drift",
			TargetRef:  "main",
			TargetHead: "cccccccc",
		},
		BaseHead: "aaaa",
		Cleanup: &conductor.CleanupOutcome{
			Status: conductor.CleanupPreserved,
		},
	}

	diag := conductor.DiagnosePlan(project, "alpha", nil)

	if diag.Status != conductor.StatusCompleted {
		t.Fatalf("status = %q", diag.Status)
	}
	if diag.Merge.Status != conductor.MergeRefused {
		t.Fatalf("merge status = %q", diag.Merge.Status)
	}
	if len(diag.AvailableActions) != 1 || diag.AvailableActions[0].Action != "retry-merge" {
		t.Fatalf("actions = %+v", diag.AvailableActions)
	}
	if !strings.Contains(diag.AvailableActions[0].Description, "target branch") {
		t.Fatalf("description = %q, want target branch guidance", diag.AvailableActions[0].Description)
	}
}

func TestDiagnosePlanMergeFailed(t *testing.T) {
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
			Error:  "ref update CAS lost",
		},
		Cleanup: &conductor.CleanupOutcome{
			Status: conductor.CleanupPreserved,
		},
	}

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	if len(diag.AvailableActions) != 1 || diag.AvailableActions[0].Action != "retry-merge" {
		t.Fatalf("actions = %+v", diag.AvailableActions)
	}
}

func TestDiagnosePlanCleanupFailed(t *testing.T) {
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
			ExecutionWorktree: &conductor.ArtifactCleanup{
				Status: conductor.CleanupFailed,
				Path:   root + "/.worktrees/alpha",
				Error:  "permission denied",
			},
		},
	}

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	if len(diag.AvailableActions) != 1 || diag.AvailableActions[0].Action != "retry-integration" {
		t.Fatalf("actions = %+v, want [retry-integration]", diag.AvailableActions)
	}
}

func TestDiagnosePlanSourceSyncFailed(t *testing.T) {
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
			SourceSyncStatus: "failed",
			SourceSyncError:  "git reset refused",
		},
		Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
	}

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	if len(diag.AvailableActions) != 1 || diag.AvailableActions[0].Action != "retry-integration" {
		t.Fatalf("actions = %+v, want [retry-integration]", diag.AvailableActions)
	}
}

func TestDiagnosePlanPendingNoRecoveryNeeded(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	if len(diag.AvailableActions) != 0 {
		t.Fatalf("pending plan should have no recovery actions, got %+v", diag.AvailableActions)
	}
	if !strings.Contains(diag.Render(), "No recovery needed") {
		t.Fatalf("render = %q", diag.Render())
	}
}

func TestDiagnosePlanFullyIntegratedNoRecoveryNeeded(t *testing.T) {
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

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	if len(diag.AvailableActions) != 0 {
		t.Fatalf("fully integrated plan should have no recovery actions, got %+v", diag.AvailableActions)
	}
	if !strings.Contains(diag.Render(), "fully integrated") {
		t.Fatalf("render should say fully integrated:\n%s", diag.Render())
	}
}

func TestDiagnosePlanMergeSucceededCleanupNil(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	// MergeSucceeded but Cleanup is nil — save failure between merge and cleanup recording
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status: conductor.StatusCompleted,
		Merge: &conductor.MergeOutcome{
			Status: conductor.MergeSucceeded,
		},
		// Cleanup intentionally nil
	}

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	if len(diag.AvailableActions) != 1 || diag.AvailableActions[0].Action != "retry-integration" {
		t.Fatalf("merge-succeeded + cleanup-nil should offer retry-integration, got %+v", diag.AvailableActions)
	}
}

func TestDiagnosePlanWithWorktreeInspection(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.MarkFailed("alpha", "exit code 1", "claude", "")

	wt := &conductor.WorktreeInspection{
		Exists:        true,
		Registered:    true,
		IsDirty:       false,
		BranchHead:    "bbbbbbbb",
		HasNewCommits: true,
	}

	diag := conductor.DiagnosePlan(project, "alpha", wt)

	if diag.Worktree == nil {
		t.Fatal("expected worktree inspection")
	}
	if !diag.Worktree.HasNewCommits {
		t.Fatal("expected has_new_commits")
	}
	if len(diag.AvailableActions) != 1 {
		t.Fatalf("actions = %+v", diag.AvailableActions)
	}
	if !strings.Contains(diag.AvailableActions[0].Description, "commits beyond base") {
		t.Fatalf("description = %q, want commits beyond base note", diag.AvailableActions[0].Description)
	}
}

func TestDiagnosePlanInterruptedDirtyWorktree(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status:       conductor.StatusInterrupted,
		Attempts:     1,
		WorktreePath: root + "/.worktrees/alpha",
	}

	wt := &conductor.WorktreeInspection{
		Exists:     true,
		Registered: true,
		IsDirty:    true,
		BranchHead: "cccc",
	}

	diag := conductor.DiagnosePlan(project, "alpha", wt)
	if !strings.Contains(diag.AvailableActions[0].Description, "uncommitted changes") {
		t.Fatalf("description = %q", diag.AvailableActions[0].Description)
	}
}

func TestDiagnosePlanRenderContainsKeyFields(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.MarkRunning("alpha")
	project.State.Plans["alpha"].WorktreePath = root + "/.worktrees/alpha"
	project.State.Plans["alpha"].Branch = "springfield/alpha"
	project.State.Plans["alpha"].BaseRef = "main"
	project.State.Plans["alpha"].BaseHead = "aaaaaaaabbbb"
	project.MarkFailed("alpha", "exit code 1", "codex", "/evidence/alpha")

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	report := diag.Render()

	checks := []string{
		"Plan: alpha",
		"Status: failed",
		"Error: exit code 1",
		"Agent: codex",
		"Evidence: /evidence/alpha",
		"Worktree: " + root + "/.worktrees/alpha",
		"Branch: springfield/alpha (base main @ aaaaaaaa)",
		"retry",
	}
	for _, check := range checks {
		if !strings.Contains(report, check) {
			t.Errorf("report missing %q:\n%s", check, report)
		}
	}
}

func TestDiagnosePlanRenderRecoveryHistory(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status:   conductor.StatusFailed,
		Error:    "exit code 1",
		Attempts: 2,
		RecoveryHistory: []conductor.RecoveryAction{
			{Action: "retry", Reason: "reset from failed to pending", At: time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)},
		},
	}

	report := conductor.DiagnosePlan(project, "alpha", nil).Render()
	if !strings.Contains(report, "Recovery history:") {
		t.Fatalf("report missing recovery history:\n%s", report)
	}
	if !strings.Contains(report, "retry") {
		t.Fatalf("report missing retry action:\n%s", report)
	}
}

func TestDiagnosePlanMergePendingNoRecoveryNeeded(t *testing.T) {
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

	diag := conductor.DiagnosePlan(project, "alpha", nil)
	if len(diag.AvailableActions) != 0 {
		t.Fatalf("merge-pending should have no recovery actions, got %+v", diag.AvailableActions)
	}
	if !strings.Contains(diag.Render(), "springfield start") {
		t.Fatalf("render = %q, should mention springfield start", diag.Render())
	}
}

func TestDiagnosePlanMissingStaleStateWithWorktreeEvidence(t *testing.T) {
	root := t.TempDir()
	writeProjectConfig(t, root)
	writeRegisteredPlanUnitConfig(t, root, []string{"alpha"})

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	// Plan failed but worktree shows commits were made
	project.State.Plans["alpha"] = &conductor.PlanState{
		Status:       conductor.StatusFailed,
		Error:        "agent-timeout",
		WorktreePath: root + "/.worktrees/alpha",
		BaseHead:     "aaaa",
	}

	wt := &conductor.WorktreeInspection{
		Exists:        true,
		Registered:    true,
		IsDirty:       false,
		BranchHead:    "bbbb",
		HasNewCommits: true,
	}

	diag := conductor.DiagnosePlan(project, "alpha", wt)
	if !diag.Worktree.HasNewCommits {
		t.Fatal("expected HasNewCommits to surface stale state")
	}
	report := diag.Render()
	if !strings.Contains(report, "commits beyond base: yes") {
		t.Fatalf("report should surface new commits:\n%s", report)
	}
}
