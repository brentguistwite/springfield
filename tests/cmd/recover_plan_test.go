package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
)

func setupPlanRecoverFixture(t *testing.T, bin, root string, ids []string) {
	t.Helper()

	writeSpringfieldConfig(t, root, "claude")

	planDir := filepath.Join(root, conductor.TrackedPlansDir)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatalf("mkdir plans: %v", err)
	}

	units := make([]conductor.PlanUnit, 0, len(ids))
	for i, id := range ids {
		if err := os.WriteFile(filepath.Join(planDir, id+".md"), []byte("# "+id+"\n"), 0o644); err != nil {
			t.Fatalf("write plan %s: %v", id, err)
		}
		units = append(units, conductor.PlanUnit{
			ID:    id,
			Path:  conductor.TrackedPlansDir + "/" + id + ".md",
			Order: i + 1,
		})
	}

	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   conductor.TrackedPlansDir,
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  units,
	})
}

func TestRecoverPlanDiagnoseFailedPlan(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupPlanRecoverFixture(t, bin, root, []string{"alpha", "beta"})

	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusFailed,
				Error:        "exit code 1",
				Agent:        "claude",
				EvidencePath: "/tmp/evidence/alpha",
				Attempts:     1,
			},
		},
	})

	output, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha", "--diagnose")
	if err != nil {
		t.Fatalf("recover --plan alpha --diagnose: %v\n%s", err, output)
	}

	for _, want := range []string{
		"Plan: alpha",
		"Status: failed",
		"Error: exit code 1",
		"Agent: claude",
		"Evidence: /tmp/evidence/alpha",
		"retry",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output:\n%s", want, output)
		}
	}
}

func TestRecoverPlanDiagnoseDoesNotModifyState(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupPlanRecoverFixture(t, bin, root, []string{"alpha"})

	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status: conductor.StatusFailed,
				Error:  "exit code 1",
			},
		},
	})

	if _, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha", "--diagnose"); err != nil {
		t.Fatalf("diagnose: %v", err)
	}

	// Reload state and verify it wasn't modified
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusFailed {
		t.Fatalf("diagnose modified state: status = %q", ps.Status)
	}
}

func TestRecoverPlanRetryFailedPlan(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupPlanRecoverFixture(t, bin, root, []string{"alpha", "beta"})

	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:   conductor.StatusFailed,
				Error:    "exit code 1",
				Agent:    "claude",
				Attempts: 1,
			},
		},
	})

	output, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha")
	if err != nil {
		t.Fatalf("recover --plan alpha: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Recovered plan") {
		t.Errorf("expected recovery message:\n%s", output)
	}
	if !strings.Contains(output, "springfield start") {
		t.Errorf("expected next-step guidance:\n%s", output)
	}

	// Verify state was updated
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusPending {
		t.Fatalf("after recovery: status = %q, want pending", ps.Status)
	}
	if len(ps.RecoveryHistory) != 1 {
		t.Fatalf("recovery history = %d, want 1", len(ps.RecoveryHistory))
	}
	if ps.RecoveryHistory[0].Action != "retry" {
		t.Fatalf("recovery action = %q", ps.RecoveryHistory[0].Action)
	}
}

func TestRecoverPlanRetryMergeRefused(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupPlanRecoverFixture(t, bin, root, []string{"alpha"})

	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status: conductor.StatusCompleted,
				Merge: &conductor.MergeOutcome{
					Status: conductor.MergeRefused,
					Reason: "target-drift",
				},
				Cleanup: &conductor.CleanupOutcome{
					Status: conductor.CleanupPreserved,
				},
			},
		},
	})

	output, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha")
	if err != nil {
		t.Fatalf("recover --plan alpha: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Recovered plan") {
		t.Errorf("expected recovery message:\n%s", output)
	}

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusCompleted {
		t.Fatalf("status should remain completed, got %q", ps.Status)
	}
	if ps.Merge != nil {
		t.Fatal("merge should be cleared")
	}
	if len(ps.RecoveryHistory) != 1 || ps.RecoveryHistory[0].Action != "retry-merge" {
		t.Fatalf("recovery history = %+v", ps.RecoveryHistory)
	}
}

func TestRecoverPlanNoActionAvailable(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupPlanRecoverFixture(t, bin, root, []string{"alpha"})

	// Pending plan — no recovery needed
	output, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha")
	if err != nil {
		t.Fatalf("recover --plan alpha: %v\n%s", err, output)
	}

	if !strings.Contains(output, "No automatic recovery actions") {
		t.Errorf("expected no-action message:\n%s", output)
	}
}

func TestRecoverPlanUnknownPlan(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupPlanRecoverFixture(t, bin, root, []string{"alpha"})

	output, err := runBinaryIn(t, bin, root, "recover", "--plan", "nonexistent")
	if err == nil {
		t.Fatalf("expected error for unknown plan, got:\n%s", output)
	}
	if !strings.Contains(output, "not registered") {
		t.Errorf("expected not-registered error:\n%s", output)
	}
}

// TestRecoverPlanBlackBoxDiagnoseAndRecover proves Springfield can diagnose a
// failed plan from persisted artifacts and guide the correct safe next step.
func TestRecoverPlanBlackBoxDiagnoseAndRecover(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupPlanRecoverFixture(t, bin, root, []string{"alpha", "beta"})

	// Simulate: alpha completed and integrated, beta failed
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status: conductor.StatusCompleted,
				Merge: &conductor.MergeOutcome{
					Status:           conductor.MergeSucceeded,
					SourceSyncStatus: "synced",
				},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
			"beta": {
				Status:       conductor.StatusFailed,
				Error:        "agent timeout after 3600s",
				Agent:        "codex",
				EvidencePath: "/tmp/evidence/beta",
				Attempts:     2,
			},
		},
	})

	// Step 1: diagnose beta
	diagOutput, err := runBinaryIn(t, bin, root, "recover", "--plan", "beta", "--diagnose")
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, diagOutput)
	}
	if !strings.Contains(diagOutput, "Status: failed") {
		t.Errorf("diagnose should show failed status:\n%s", diagOutput)
	}
	if !strings.Contains(diagOutput, "agent timeout") {
		t.Errorf("diagnose should show error:\n%s", diagOutput)
	}
	if !strings.Contains(diagOutput, "retry") {
		t.Errorf("diagnose should suggest retry:\n%s", diagOutput)
	}

	// Step 2: recover beta
	recoverOutput, err := runBinaryIn(t, bin, root, "recover", "--plan", "beta")
	if err != nil {
		t.Fatalf("recover: %v\n%s", err, recoverOutput)
	}
	if !strings.Contains(recoverOutput, "Recovered plan") {
		t.Errorf("expected recovery confirmation:\n%s", recoverOutput)
	}

	// Step 3: verify state is now ready for re-execution
	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}

	betaState := project.State.Plans["beta"]
	if betaState.Status != conductor.StatusPending {
		t.Fatalf("beta should be pending after recovery, got %q", betaState.Status)
	}
	if betaState.Error != "" {
		t.Fatalf("error should be cleared, got %q", betaState.Error)
	}
	if len(betaState.RecoveryHistory) != 1 {
		t.Fatalf("recovery history = %d", len(betaState.RecoveryHistory))
	}

	// Verify alpha is untouched
	alphaState := project.State.Plans["alpha"]
	if !alphaState.IsIntegrated() {
		t.Fatal("alpha should still be integrated")
	}

	// Step 4: status should now show beta as pending, ready for start
	statusOutput, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, statusOutput)
	}
	if !strings.Contains(statusOutput, "pending") {
		t.Errorf("status should show beta as pending:\n%s", statusOutput)
	}
}

func TestRecoverPlanInterruptedViaStaleRunning(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	setupPlanRecoverFixture(t, bin, root, []string{"alpha"})

	// Simulate: alpha left in running state (process died)
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusRunning,
				Attempts:     1,
				WorktreePath: root + "/.worktrees/alpha",
			},
		},
	})

	// Diagnose should show interrupted (stale-running normalized)
	output, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha", "--diagnose")
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, output)
	}
	if !strings.Contains(output, "interrupted") {
		t.Errorf("should show interrupted (normalized from running):\n%s", output)
	}

	// Recover should work
	output, err = runBinaryIn(t, bin, root, "recover", "--plan", "alpha")
	if err != nil {
		t.Fatalf("recover: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Recovered plan") {
		t.Errorf("expected recovery:\n%s", output)
	}

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	if project.State.Plans["alpha"].Status != conductor.StatusPending {
		t.Fatalf("status = %q", project.State.Plans["alpha"].Status)
	}
}

// TestRecoverPlanIntegrationReEntryPath proves that retry-integration recovery
// preserves merge state so the next springfield start follows the MergeSucceeded
// re-entry path instead of target-drifting because merge state was erased.
func TestRecoverPlanIntegrationReEntryPath(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)

	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlan(t, dir, "alpha", "Test plan")
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	baseHead := gitOut(t, dir, "rev-parse", "main")

	// Create plan branch with a commit ahead of main
	gitMust(t, dir, "branch", "springfield/alpha", "main")
	wtPath := filepath.Join(dir, ".worktrees", "alpha")
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		t.Fatalf("mkdir worktrees: %v", err)
	}
	gitMust(t, dir, "worktree", "add", wtPath, "springfield/alpha")
	if err := os.WriteFile(filepath.Join(wtPath, "feature.txt"), []byte("agent work\n"), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	gitMust(t, wtPath, "add", ".")
	gitMust(t, wtPath, "commit", "-m", "agent commit")
	planHead := gitOut(t, wtPath, "rev-parse", "HEAD")

	// Simulate published merge: advance main to plan head
	gitMust(t, dir, "update-ref", "refs/heads/main", planHead, baseHead)

	// Create merge worktree so cleanup has something to remove
	mergeWtPath := filepath.Join(dir, ".worktrees", ".merges", "alpha")
	if err := os.MkdirAll(filepath.Dir(mergeWtPath), 0o755); err != nil {
		t.Fatalf("mkdir merge wt: %v", err)
	}
	gitMust(t, dir, "worktree", "add", "--detach", mergeWtPath, "main")

	// Write state: MergeSucceeded + CleanupFailed
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusCompleted,
				Attempts:     1,
				WorktreePath: wtPath,
				Branch:       "springfield/alpha",
				BaseRef:      "main",
				BaseHead:     baseHead,
				PlanHead:     planHead,
				Merge: &conductor.MergeOutcome{
					Status:        conductor.MergeSucceeded,
					Mode:          "ff-only",
					Reason:        "merge-ok",
					TargetRef:     "main",
					TargetHead:    baseHead,
					PostMergeHead: planHead,
					WorktreePath:  mergeWtPath,
				},
				Cleanup: &conductor.CleanupOutcome{
					Status: conductor.CleanupFailed,
					MergeWorktree: &conductor.ArtifactCleanup{
						Status: conductor.CleanupFailed,
						Path:   mergeWtPath,
						Error:  "simulated failure",
					},
					ExecutionWorktree: &conductor.ArtifactCleanup{
						Status: conductor.CleanupFailed,
						Path:   wtPath,
						Error:  "simulated failure",
					},
					PlanBranch: &conductor.ArtifactCleanup{
						Status: conductor.CleanupFailed,
						Branch: "springfield/alpha",
						Error:  "simulated failure",
					},
				},
			},
		},
	})

	// Step 1: Diagnose → should offer retry-integration, NOT retry-merge
	diagOutput, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha", "--diagnose")
	if err != nil {
		t.Fatalf("diagnose: %v\n%s", err, diagOutput)
	}
	if !strings.Contains(diagOutput, "retry-integration") {
		t.Errorf("diagnose should offer retry-integration:\n%s", diagOutput)
	}

	// Step 2: Recover
	recoverOutput, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha")
	if err != nil {
		t.Fatalf("recover: %v\n%s", err, recoverOutput)
	}

	// Verify merge state preserved
	project, err := conductor.LoadProject(dir)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	ps := project.State.Plans["alpha"]
	if ps.Merge == nil {
		t.Fatal("merge state should be preserved after retry-integration")
	}
	if ps.Merge.Status != conductor.MergeSucceeded {
		t.Fatalf("merge status = %q, want succeeded", ps.Merge.Status)
	}
	if ps.Merge.PostMergeHead != planHead {
		t.Fatalf("PostMergeHead lost: %q != %q", ps.Merge.PostMergeHead, planHead)
	}

	// Step 3: Start → should follow MergeSucceeded re-entry (not target-drift)
	startOutput, err := runBinaryIn(t, bin, dir, "start")
	if err != nil {
		t.Fatalf("start after retry-integration: %v\n%s", err, startOutput)
	}
	if !strings.Contains(startOutput, "re-running merge integration") {
		t.Errorf("should enter merge-only re-run:\n%s", startOutput)
	}
	if strings.Contains(startOutput, "target-drift") || strings.Contains(startOutput, "refused") {
		t.Fatalf("should NOT hit target-drift:\n%s", startOutput)
	}
	if !strings.Contains(startOutput, "prior merge already succeeded") {
		t.Errorf("should follow MergeSucceeded re-entry path:\n%s", startOutput)
	}

	// After start: plan should be fully integrated
	project2, err := conductor.LoadProject(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !project2.State.Plans["alpha"].IsIntegrated() {
		t.Fatal("plan should be fully integrated after re-entry")
	}
}

func TestRecoverOrphanStillWorks(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "ghost"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	output, err := runBinaryIn(t, bin, root, "recover")
	if err != nil {
		t.Fatalf("orphan recover should still work: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Archived orphan batch") {
		t.Errorf("expected orphan archive message:\n%s", output)
	}
}
