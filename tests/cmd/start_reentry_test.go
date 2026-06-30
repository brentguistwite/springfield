package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

// TestSpringfieldStartBatchReentryIntegratesWithoutRedispatch covers the
// round-1 deadlock fix: a per-plan batch plan that already finished execution
// (Status=Completed) but was not yet integrated — e.g. a crash between exec and
// integration — must be driven straight through integration on the next start,
// NOT re-dispatched to the agent (which would hit preflight-already-completed
// and deadlock). The standalone branch is retained and the batch completes.
func TestSpringfieldStartBatchReentryIntegratesWithoutRedispatch(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeProjectConfig(t, dir, "claude")

	// Compile a 1-plan per-plan batch.
	env := prd.BatchPRDEnvelope{
		Title:  "Re-entry batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-1"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-1", "Plan One")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env)); err != nil {
		t.Fatalf("compile batch: %v\n%s", err, out)
	}

	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")
	mainSHA := gitOut(t, dir, "rev-parse", "main")
	// The plan branch already exists (cut during the crashed first attempt).
	gitMust(t, dir, "branch", "springfield/plan-1")

	// Seed: per-plan mode stamped, and plan-1 Completed-but-not-integrated.
	run := readRunJSON(t, dir)
	run.BatchMode = "per-plan"
	run.BatchBase = "main"
	if err := batch.WriteRun(dir, run); err != nil {
		t.Fatalf("write run.json: %v", err)
	}
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"plan-1": {
				Status:       conductor.StatusCompleted,
				Branch:       "springfield/plan-1",
				WorktreePath: filepath.Join(dir, ".worktrees", "plan-1"), // absent → idempotent skip
				BaseRef:      "main",
				BaseHead:     mainSHA,
				// Merge=pending is what SinglePlan records on completion; the
				// crash struck before integration, so IsIntegrated() is false and
				// the re-entry must drive integration (NOT re-dispatch the agent).
				Merge: &conductor.MergeOutcome{Status: conductor.MergePending},
			},
		},
	})

	// A canary agent that must NOT be invoked on the re-entry path.
	fakeBinDir := filepath.Join(dir, "bin")
	canary := filepath.Join(dir, "agent-was-invoked")
	installCanaryAgent(t, fakeBinDir, "claude", canary)

	output, err := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	if err != nil {
		t.Fatalf("start (re-entry): %v\n%s", err, output)
	}
	if !strings.Contains(output, "resuming integration") {
		t.Fatalf("expected the re-entry banner, got:\n%s", output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Fatalf("batch must complete, got:\n%s", output)
	}
	// Agent must NOT have been dispatched.
	if _, statErr := os.Stat(canary); !os.IsNotExist(statErr) {
		t.Fatalf("agent must not be invoked on integration re-entry; canary stat err=%v", statErr)
	}
	// Standalone branch retained; base untouched.
	if strings.TrimSpace(gitOut(t, dir, "branch", "--list", "springfield/plan-1")) == "" {
		t.Fatal("per-plan branch must be retained")
	}
	if gitOut(t, dir, "rev-parse", "main") != mainSHA {
		t.Fatal("per-plan mode must not advance the base")
	}
	// Cursor cleared.
	if _, statErr := os.Stat(filepath.Join(dir, ".springfield", "run.json")); !os.IsNotExist(statErr) {
		t.Fatalf("run.json must be cleared after completion, stat err=%v", statErr)
	}
}

// TestSpringfieldStartUnstampedInProgressWarnsWhenPerPlanDropped covers the
// in-scope mitigation for the provenance gap: when --per-plan-branches is passed
// against an UNSTAMPED batch that already carries non-pending plan state, the
// mode is locked to consolidate (correct, conservative) but the dropped request
// must NOT be silent — start warns on stderr. The plan is seeded Completed (with
// an absent worktree) so the start path routes through integration and never
// dispatches the agent, keeping the assertion deterministic.
func TestSpringfieldStartUnstampedInProgressWarnsWhenPerPlanDropped(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "Reused-slug batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-1"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-1", "Plan One")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env)); err != nil {
		t.Fatalf("compile batch: %v\n%s", err, out)
	}
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	// run.json is left as-compiled — UNSTAMPED (BatchMode == "") — and a
	// non-pending plan state is seeded for the reused slug: the exact
	// stale-PlanState shape that drives the suppression path.
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"plan-1": {
				Status:       conductor.StatusCompleted,
				Branch:       "springfield/plan-1",
				WorktreePath: filepath.Join(dir, ".worktrees", "plan-1"), // absent → Integrate fails fast, no dispatch
				BaseRef:      "main",
				BaseHead:     "deadbeefcafef00ddeadbeefcafef00ddeadbeef",
				Merge:        &conductor.MergeOutcome{Status: conductor.MergePending},
			},
		},
	})

	fakeBinDir := filepath.Join(dir, "bin")
	canary := filepath.Join(dir, "agent-was-invoked")
	installCanaryAgent(t, fakeBinDir, "claude", canary)

	output, _ := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start", "--per-plan-branches")
	if !strings.Contains(output, "--per-plan-branches ignored") {
		t.Fatalf("dropped per-plan flag must warn, got:\n%s", output)
	}
	if strings.Contains(output, "Branch mode: per-plan") {
		t.Fatalf("mode must be locked to consolidate, not per-plan:\n%s", output)
	}
	if _, statErr := os.Stat(canary); !os.IsNotExist(statErr) {
		t.Fatalf("agent must not be dispatched on this path; canary stat err=%v", statErr)
	}
}

// TestSpringfieldStartBatchReentryConsolidateRoutesToIntegrate covers the
// consolidate side of the round-1 "both modes" re-entry fix: a Completed-but-
// not-integrated plan in a consolidate batch must be driven through Integrate
// (the merge phase), NOT re-dispatched to the agent and NOT rejected with
// preflight-already-completed. The seeded state is deliberately broken (absent
// worktree) so Integrate fails fast — the point is to prove the routing, not a
// successful merge.
func TestSpringfieldStartBatchReentryConsolidateRoutesToIntegrate(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "Re-entry consolidate",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-1"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-1", "Plan One")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env)); err != nil {
		t.Fatalf("compile batch: %v\n%s", err, out)
	}
	gitMust(t, dir, "add", ".")
	gitMust(t, dir, "commit", "-m", "scaffold")

	run := readRunJSON(t, dir)
	run.BatchMode = "consolidate"
	if err := batch.WriteRun(dir, run); err != nil {
		t.Fatalf("write run.json: %v", err)
	}
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"plan-1": {
				Status:       conductor.StatusCompleted,
				Branch:       "springfield/plan-1",
				WorktreePath: filepath.Join(dir, ".worktrees", "plan-1"), // absent → Integrate fails fast
				BaseRef:      "main",
				BaseHead:     "deadbeefcafef00ddeadbeefcafef00ddeadbeef",
				Merge:        &conductor.MergeOutcome{Status: conductor.MergePending},
			},
		},
	})

	fakeBinDir := filepath.Join(dir, "bin")
	canary := filepath.Join(dir, "agent-was-invoked")
	installCanaryAgent(t, fakeBinDir, "claude", canary)

	output, _ := runBinaryInWithEnv(t, bin, dir,
		[]string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")},
		"start")
	// Re-entry must have fired (banner) and routed to the merge phase, NOT to
	// SinglePlan (which would print preflight-already-completed).
	if !strings.Contains(output, "resuming integration") {
		t.Fatalf("expected the re-entry banner, got:\n%s", output)
	}
	if strings.Contains(output, "preflight-already-completed") {
		t.Fatalf("re-entry must NOT route through SinglePlan, got:\n%s", output)
	}
	if _, statErr := os.Stat(canary); !os.IsNotExist(statErr) {
		t.Fatalf("agent must not be invoked on consolidate re-entry; canary stat err=%v", statErr)
	}
}
