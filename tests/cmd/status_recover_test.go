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

// TestStatusKeepsFatalErrorForNeedsHumanHalt pins the round-2 adversarial-review
// fix (R3F2): a batch halted on StatusNeedsHuman writes FatalError to run.json
// the same way StatusFailed does (cmd/start.go), but the previous version of
// batchHasFailedPlan only checked StatusFailed — silently suppressing the error
// message for needs-human halts even though the plan still needs operator
// attention. With the broadened predicate, both halting statuses keep the error
// visible until they are actually resolved.
func TestStatusKeepsFatalErrorForNeedsHumanHalt(t *testing.T) {
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
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "batch-001", FatalError: "pre-merge review halted: needs human attention"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {Status: conductor.StatusNeedsHuman, Error: "halt verdict from reviewer", Attempts: 1},
		},
	})

	out, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Fatal error") {
		t.Fatalf("needs-human halts must keep the batch-level fatal error visible (operator still owes attention):\n%s", out)
	}
}

// TestStatusSuppressesFatalErrorAfterMarkCompleted pins D1 for the A9 path:
// `recover --plan X --mark-completed` flips a failed plan to StatusCompleted,
// and `status` must also suppress the stale batch-level fatal error in that
// case. The current implementation gates suppression on "no plan in batch is
// StatusFailed", which a Completed plan satisfies — but absent a dedicated
// test, a future change widening the gate could silently regress.
func TestStatusSuppressesFatalErrorAfterMarkCompleted(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	plans := []conductor.PlanUnit{
		{ID: "alpha", Title: "Alpha", Path: ".springfield/plans/alpha/prd.json", Order: 1},
	}
	// prd.json with a passing story — mark-completed validates against this.
	writePRDJSONAllPassed(t, root, "alpha")
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
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "batch-001", FatalError: "plan alpha crashed: boom"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {Status: conductor.StatusFailed, Error: "boom", Attempts: 1},
		},
	})

	// Before: status surfaces the fatal error.
	pre, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status (pre): %v\n%s", err, pre)
	}
	if !strings.Contains(pre, "Fatal error") {
		t.Fatalf("expected fatal error before mark-completed:\n%s", pre)
	}

	// Mark alpha completed (its prd.json has all stories passing).
	if out, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha", "--mark-completed"); err != nil {
		t.Fatalf("recover --plan alpha --mark-completed: %v\n%s", err, out)
	}

	// After: stale fatal error must be suppressed even though the resolution
	// was completion (not retry-to-pending).
	post, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status (post): %v\n%s", err, post)
	}
	if strings.Contains(post, "Fatal error") {
		t.Fatalf("stale fatal error not suppressed after mark-completed:\n%s", post)
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
