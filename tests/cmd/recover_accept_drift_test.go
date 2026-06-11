package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor"
	"springfield/internal/features/conductor/planrun"
)

// TestRecoverAcceptDriftHelpDocumented proves --accept-drift is surfaced in
// --help, satisfying the discoverability acceptance criterion.
func TestRecoverAcceptDriftHelpDocumented(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	output, err := runBinaryIn(t, bin, root, "recover", "--help")
	if err != nil {
		t.Fatalf("recover --help: %v\n%s", err, output)
	}
	if !strings.Contains(output, "--accept-drift") {
		t.Errorf("--help should document --accept-drift:\n%s", output)
	}
}

// TestRecoverAcceptDriftRecordsCurrentDigest is the unit-level proof of A10:
// a plan whose recorded digest no longer matches its current inputs has the
// recorded value overwritten with the freshly computed digest and is reset to
// pending. No git/worktree is needed — this isolates the digest-set + reset
// flow at the CLI surface.
func TestRecoverAcceptDriftRecordsCurrentDigest(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")
	writeRegisteredPlansBinary(t, root, []registeredPlan{
		{ID: "alpha", Title: "Implement alpha", Order: 1},
	})

	unit := conductor.PlanUnit{ID: "alpha", Path: ".springfield/plans/alpha.md"}
	current, err := planrun.InputDigest(root, unit)
	if err != nil {
		t.Fatalf("compute current digest: %v", err)
	}

	// Record a deliberately stale digest so accept-drift has drift to clear.
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:      conductor.StatusFailed,
				Error:       "agent timeout",
				ExitReason:  "preflight-input-drift",
				InputDigest: "sha256:stale",
			},
		},
	})

	out, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha", "--accept-drift")
	if err != nil {
		t.Fatalf("recover --accept-drift: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Accepted input drift") {
		t.Errorf("expected accept-drift confirmation:\n%s", out)
	}
	if !strings.Contains(out, "springfield start") {
		t.Errorf("expected next-step guidance:\n%s", out)
	}

	project, err := conductor.LoadProject(root)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	ps := project.State.Plans["alpha"]
	if ps.InputDigest != current {
		t.Fatalf("recorded digest = %q, want current %q", ps.InputDigest, current)
	}
	if ps.Status != conductor.StatusPending {
		t.Fatalf("status = %q, want pending", ps.Status)
	}
	if ps.Error != "" || ps.ExitReason != "" {
		t.Errorf("accept-drift must clear failure state, got Error=%q ExitReason=%q", ps.Error, ps.ExitReason)
	}
}

// TestRecoverResetDiscardsWorktreeAndBranch pins the dogfood #6 full-cleanup
// path: --reset removes the worktree AND deletes the springfield/<plan> branch
// (not just the worktree), so the next start re-creates from base without the
// "branch already exists" / dangling-registration collision.
func TestRecoverResetDiscardsWorktreeAndBranch(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Implement alpha", Order: 1},
	})
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	wt := filepath.Join(dir, ".worktrees", "alpha")
	gitMust(t, dir, "worktree", "add", "-b", "springfield/alpha", wt, "main")
	baseHead := strings.TrimSpace(gitOut(t, dir, "rev-parse", "main"))

	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusFailed,
				Attempts:     1,
				WorktreePath: wt,
				Branch:       "springfield/alpha",
				BaseRef:      "main",
				BaseHead:     baseHead,
				ExitReason:   "preflight-input-drift",
				InputDigest:  "sha256:stale",
			},
		},
	})

	out, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha", "--reset")
	if err != nil {
		t.Fatalf("recover --reset: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Reset plan") {
		t.Errorf("expected reset confirmation:\n%s", out)
	}

	// Worktree dir is gone on disk (symlink-agnostic, unlike a porcelain string
	// match: macOS git reports /private/var while wt is /var).
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Errorf("worktree dir should be removed after reset, stat err = %v", statErr)
	}
	// Branch is gone.
	if bl := strings.TrimSpace(gitOut(t, dir, "branch", "--list", "springfield/alpha")); bl != "" {
		t.Errorf("branch still exists after reset: %q", bl)
	}

	project, err := conductor.LoadProject(dir)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	ps := project.State.Plans["alpha"]
	if ps.Status != conductor.StatusPending {
		t.Errorf("status = %q, want pending", ps.Status)
	}
	if ps.WorktreePath != "" || ps.Branch != "" {
		t.Errorf("worktree refs not cleared: path=%q branch=%q", ps.WorktreePath, ps.Branch)
	}
}

// TestRecoverAcceptDriftLetsStartReachDispatch is the full A10 integration: an
// interrupted plan with a live worktree drifts because an operator deliberately
// added a guidance file (AGENTS.md), --accept-drift records the new digest, and
// the next springfield start resumes the worktree and dispatches the agent
// instead of refusing with preflight-input-drift.
func TestRecoverAcceptDriftLetsStartReachDispatch(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeSpringfieldConfig(t, dir, "claude")
	writeRegisteredPlansBinary(t, dir, []registeredPlan{
		{ID: "alpha", Title: "Implement alpha", Order: 1},
	})

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	wt := filepath.Join(dir, ".worktrees", "alpha")
	gitMust(t, dir, "worktree", "add", "-b", "springfield/alpha", wt, "main")
	baseHead := strings.TrimSpace(gitOut(t, dir, "rev-parse", "main"))

	unit := conductor.PlanUnit{ID: "alpha", Path: ".springfield/plans/alpha.md"}
	digestBefore, err := planrun.InputDigest(dir, unit)
	if err != nil {
		t.Fatalf("compute digest before mutation: %v", err)
	}

	// Record the pre-mutation digest as the last-attempt digest.
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status:       conductor.StatusInterrupted,
				Attempts:     1,
				WorktreePath: wt,
				Branch:       "springfield/alpha",
				BaseRef:      "main",
				BaseHead:     baseHead,
				ExitReason:   conductor.ExitInterruptedProcessExit,
				InputDigest:  digestBefore,
			},
		},
	})

	// Operator deliberately adds project guidance — a real input change the
	// digest correctly flags as drift.
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Guidance\nDo it well.\n"), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	gitMust(t, dir, "add", "AGENTS.md")
	gitMust(t, dir, "commit", "-m", "add guidance")

	// Precondition: the mutation actually produced drift.
	digestAfter, err := planrun.InputDigest(dir, unit)
	if err != nil {
		t.Fatalf("compute digest after mutation: %v", err)
	}
	if digestAfter == digestBefore {
		t.Fatalf("setup: input mutation did not change the digest (%q)", digestAfter)
	}

	// Accept the drift.
	out, err := runBinaryIn(t, bin, dir, "recover", "--plan", "alpha", "--accept-drift")
	if err != nil {
		t.Fatalf("recover --accept-drift: %v\n%s", err, out)
	}

	project, err := conductor.LoadProject(dir)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	ps := project.State.Plans["alpha"]
	if ps.InputDigest != digestAfter {
		t.Fatalf("recorded digest = %q, want post-mutation %q", ps.InputDigest, digestAfter)
	}
	if ps.Status != conductor.StatusPending {
		t.Fatalf("status = %q, want pending after accept-drift", ps.Status)
	}

	// Next start must reach agent dispatch (worktree reuse), not drift refusal.
	// We assert on dispatch markers, not on the exit code: committing the
	// guidance change advanced main past the recorded base_head, so the
	// downstream merge phase legitimately refuses with target-drift. That
	// refusal is a scaffold artifact (retry-merge territory), entirely outside
	// A10's contract, which ends once the agent is dispatched without
	// preflight-input-drift.
	fakeBinDir := filepath.Join(dir, "bin")
	installBranchAwareAgent(t, fakeBinDir, "claude")
	startOut, _ := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if strings.Contains(startOut, "preflight-input-drift") {
		t.Fatalf("start still refused with input drift after accept-drift:\n%s", startOut)
	}
	if !strings.Contains(startOut, "dispatching agent") {
		t.Fatalf("start did not dispatch the agent after accept-drift:\n%s", startOut)
	}
	if !strings.Contains(startOut, "reusing worktree") || !strings.Contains(startOut, "resume-same-inputs") {
		t.Fatalf("start did not resume the recorded worktree on stable inputs:\n%s", startOut)
	}
	if !strings.Contains(startOut, "Plan: alpha") {
		t.Fatalf("start did not surface the dispatched plan:\n%s", startOut)
	}
}
