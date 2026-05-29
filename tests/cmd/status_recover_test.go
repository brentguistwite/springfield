package cmd_test

import (
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
)

// TestStatusSuppressesFatalErrorAfterRecover is the end-to-end proof of D1:
// after `recover --plan X` resets a failed plan, `status` must no longer surface
// the prior batch-level fatal error that referred to X.
func TestStatusSuppressesFatalErrorAfterRecover(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	plans := []conductor.PlanUnit{
		{ID: "alpha", Title: "Alpha", Path: ".springfield/plans/alpha/prd.json", Order: 1},
	}
	writePRDJSON(t, root, "alpha")
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  plans,
	})

	writeActiveBatchBinaryN(t, root, "batch-001", "Active Batch", []string{"alpha"})
	// Record a prior terminal failure in run.json (as start.go would on halt).
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "batch-001", FatalError: "plan alpha crashed: boom"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {Status: conductor.StatusFailed, Error: "boom", Attempts: 1},
		},
	})

	// Before recover: status surfaces the fatal error (the data is really there).
	pre, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status (pre): %v\n%s", err, pre)
	}
	if !strings.Contains(pre, "Fatal error") {
		t.Fatalf("expected fatal error before recover:\n%s", pre)
	}

	// Recover alpha back to pending.
	if out, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha"); err != nil {
		t.Fatalf("recover --plan alpha: %v\n%s", err, out)
	}

	// After recover: the stale fatal error must be gone.
	post, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status (post): %v\n%s", err, post)
	}
	if strings.Contains(post, "Fatal error") {
		t.Fatalf("stale fatal error not suppressed after recover:\n%s", post)
	}
}

// TestStatusKeepsFatalErrorForUnrecoveredPlan covers the multi-plan AC: with two
// failed plans, recovering one leaves the other's fatal error visible.
func TestStatusKeepsFatalErrorForUnrecoveredPlan(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	plans := []conductor.PlanUnit{
		{ID: "alpha", Title: "Alpha", Path: ".springfield/plans/alpha/prd.json", Order: 1},
		{ID: "beta", Title: "Beta", Path: ".springfield/plans/beta/prd.json", Order: 2},
	}
	writePRDJSON(t, root, "alpha")
	writePRDJSON(t, root, "beta")
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  plans,
	})

	writeActiveBatchBinaryN(t, root, "batch-001", "Active Batch", []string{"alpha", "beta"})
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "batch-001", FatalError: "plan beta crashed: boom"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {Status: conductor.StatusFailed, Error: "boom", Attempts: 1},
			"beta":  {Status: conductor.StatusFailed, Error: "boom", Attempts: 1},
		},
	})

	// Recover only alpha; beta stays failed.
	if out, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha"); err != nil {
		t.Fatalf("recover --plan alpha: %v\n%s", err, out)
	}

	out, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Fatal error") {
		t.Fatalf("fatal error must remain while beta is still failed:\n%s", out)
	}
}
