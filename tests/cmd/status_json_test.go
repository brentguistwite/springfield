package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
)

func TestStatusJSON_ActiveBatch(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	plans := []conductor.PlanUnit{
		{ID: "01", Title: "Plan 01", Path: ".springfield/plans/01/prd.json", Order: 1},
	}
	writePRDJSON(t, root, "01")
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  plans,
	})

	writeActiveBatchBinaryN(t, root, "batch-001", "Active Batch", []string{"01"})
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"01": {Status: conductor.StatusRunning},
		},
	})

	output, err := runBinaryIn(t, bin, root, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, output)
	}

	var v map[string]any
	if err := json.Unmarshal([]byte(output), &v); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, output)
	}
	if v["schema_version"].(float64) != 1 {
		t.Fatalf("schema_version = %v", v["schema_version"])
	}
	if v["state"] != "active" {
		t.Fatalf("state = %v", v["state"])
	}
	plans2, ok := v["plans"].([]any)
	if !ok || len(plans2) == 0 {
		t.Fatalf("plans missing/empty: %v", v["plans"])
	}
	// Finding 4: assert projected plan fields and non-nil progress/flags.
	plan0, ok := plans2[0].(map[string]any)
	if !ok {
		t.Fatalf("plans[0] is not an object: %T", plans2[0])
	}
	// Audit #1: liveness detection. The plan is persisted as StatusRunning but
	// no live springfield process holds the control-plane lock during this test
	// (no `start` running), so lock.Inspect finds no holder and the plan is
	// correctly projected as "stalled" — proving the liveness probe end to end.
	if plan0["status"] != "stalled" {
		t.Fatalf("plans[0].status = %v, want stalled (no live process owns the lock)", plan0["status"])
	}
	if plan0["title"] != "Plan 01" {
		t.Fatalf("plans[0].title = %v, want Plan 01", plan0["title"])
	}
	if v["progress"] == nil {
		t.Fatal("progress must be non-nil for active batch with state")
	}
	if v["flags"] == nil {
		t.Fatal("flags must be non-nil for active batch")
	}
}

// TestStatusParity_InterruptedStalled proves the text and JSON surfaces agree
// on a previously-divergent case: an interrupted plan with no live process. Both
// route through statusview.ComposeStatus, so text renders "Stalled" and JSON
// renders status:"stalled" — never one classification in one surface and a
// different one in the other.
func TestStatusParity_InterruptedStalled(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	plans := []conductor.PlanUnit{
		{ID: "01", Title: "Plan 01", Path: ".springfield/plans/01/prd.json", Order: 1},
	}
	writePRDJSON(t, root, "01")
	writeConductorConfigBinary(t, root, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits:                  plans,
	})
	writeActiveBatchBinaryN(t, root, "batch-001", "Active Batch", []string{"01"})
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"01": {Status: conductor.StatusInterrupted},
		},
	})

	// Text surface: must render Stalled (not "Next:" / pending).
	textOut, err := runBinaryIn(t, bin, root, "status")
	if err != nil {
		t.Fatalf("status (text): %v\n%s", err, textOut)
	}
	if !strings.Contains(textOut, "Stalled: 01") {
		t.Fatalf("text must render interrupted+dead plan as Stalled; got:\n%s", textOut)
	}

	// JSON surface: must classify the same plan as stalled.
	jsonOut, err := runBinaryIn(t, bin, root, "status", "--json")
	if err != nil {
		t.Fatalf("status --json: %v\n%s", err, jsonOut)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonOut)
	}
	plansArr, _ := v["plans"].([]any)
	if len(plansArr) == 0 {
		t.Fatalf("no plans in JSON: %v", v["plans"])
	}
	plan0, _ := plansArr[0].(map[string]any)
	if plan0["status"] != "stalled" {
		t.Fatalf("JSON must classify interrupted+dead plan as stalled; got %v", plan0["status"])
	}
}

// TestStatusJSON_Idle verifies that a project with no active batch emits
// state == "idle" and plans == null via --json.
func TestStatusJSON_Idle(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")
	// No run.json written → no active batch.

	output, err := runBinaryIn(t, bin, root, "status", "--json")
	if err != nil {
		t.Fatalf("status --json idle: %v\n%s", err, output)
	}

	var v map[string]any
	if err := json.Unmarshal([]byte(output), &v); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, output)
	}
	if v["state"] != "idle" {
		t.Fatalf("state = %v, want idle", v["state"])
	}
	// plans must be null, not an array.
	if _, ok := v["plans"].([]any); ok {
		t.Fatalf("plans must be null for idle state; got array: %v", v["plans"])
	}
	if v["plans"] != nil {
		t.Fatalf("plans must be null for idle state; got: %v", v["plans"])
	}
}

// TestStatusJSON_Orphan verifies that when run.json references a batch whose
// batch.json is missing, --json emits state == "orphan" and plans == null.
func TestStatusJSON_Orphan(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	writeSpringfieldConfig(t, root, "claude")

	// Write run.json referencing a batch ID that has no batch.json.
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "ghost-batch-001"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	// Ensure the batch directory exists but has NO batch.json inside it.
	ghostDir := filepath.Join(root, ".springfield", "batches", "ghost-batch-001")
	if err := os.MkdirAll(ghostDir, 0o755); err != nil {
		t.Fatalf("mkdir ghost batch dir: %v", err)
	}

	output, err := runBinaryIn(t, bin, root, "status", "--json")
	if err != nil {
		t.Fatalf("status --json orphan: %v\n%s", err, output)
	}

	var v map[string]any
	if err := json.Unmarshal([]byte(output), &v); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, output)
	}
	if v["state"] != "orphan" {
		t.Fatalf("state = %v, want orphan", v["state"])
	}
	// plans must be null for orphan.
	if v["plans"] != nil {
		t.Fatalf("plans must be null for orphan state; got: %v", v["plans"])
	}
}

// TestStatusJSON_SuppressesFatalErrorAfterRecover is the --json parallel of
// TestStatusSuppressesFatalErrorAfterRecover: after `recover --plan X` resets
// a failed plan, `status --json` must emit flags.fatal_error == null.
// Also asserts flags.fatal_error IS non-null while the plan is still failed.
func TestStatusJSON_SuppressesFatalErrorAfterRecover(t *testing.T) {
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
	if err := batch.WriteRun(root, batch.Run{ActiveBatchID: "batch-001", FatalError: "plan alpha crashed: boom"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	writeConductorStateBinary(t, root, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {Status: conductor.StatusFailed, Error: "boom", Attempts: 1},
		},
	})

	// While plan is still failed: flags.fatal_error must be non-null.
	preOut, err := runBinaryIn(t, bin, root, "status", "--json")
	if err != nil {
		t.Fatalf("status --json (pre): %v\n%s", err, preOut)
	}
	var pre map[string]any
	if err := json.Unmarshal([]byte(preOut), &pre); err != nil {
		t.Fatalf("pre output is not valid JSON: %v\n%s", err, preOut)
	}
	flags, _ := pre["flags"].(map[string]any)
	if flags == nil {
		t.Fatal("flags must be present in active state")
	}
	if flags["fatal_error"] == nil {
		t.Fatalf("flags.fatal_error must be non-null while plan is still failed; got nil")
	}

	// Recover alpha back to pending.
	if out, err := runBinaryIn(t, bin, root, "recover", "--plan", "alpha"); err != nil {
		t.Fatalf("recover --plan alpha: %v\n%s", err, out)
	}

	// After recover: flags.fatal_error must be null.
	postOut, err := runBinaryIn(t, bin, root, "status", "--json")
	if err != nil {
		t.Fatalf("status --json (post): %v\n%s", err, postOut)
	}
	var post map[string]any
	if err := json.Unmarshal([]byte(postOut), &post); err != nil {
		t.Fatalf("post output is not valid JSON: %v\n%s", err, postOut)
	}
	postFlags, _ := post["flags"].(map[string]any)
	if postFlags == nil {
		t.Fatal("flags must be present after recover")
	}
	if postFlags["fatal_error"] != nil {
		t.Fatalf("flags.fatal_error must be null after recover; got %v", postFlags["fatal_error"])
	}
}
