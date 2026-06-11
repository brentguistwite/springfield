package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/conductor"
	"springfield/internal/features/prd"
)

func TestSpringfieldRecoverHelp(t *testing.T) {
	output, err := runSpringfield(t, "recover", "--help")
	if err != nil {
		t.Fatalf("recover --help: %v\n%s", err, output)
	}
	if !strings.Contains(output, "orphaned batch") {
		t.Errorf("expected orphan wording in help, got:\n%s", output)
	}
	if !strings.Contains(output, "--plan") {
		t.Errorf("expected --plan flag in help, got:\n%s", output)
	}
}

func TestSpringfieldRecoverOnOrphanArchivesAndClears(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Manufacture an orphan: run.json pointing at nothing.
	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: "ghost", FatalError: "original error"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "recover")
	if err != nil {
		t.Fatalf("recover failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Archived orphan batch") {
		t.Errorf("expected archive message, got:\n%s", output)
	}

	if _, ok, _ := batch.ReadRun(dir); ok {
		t.Error("run.json should be cleared")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".springfield", "archive"))
	if len(entries) != 1 {
		t.Errorf("archive entries = %d, want 1", len(entries))
	}
}

func TestSpringfieldRecoverIdempotent(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: "ghost"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	if _, err := runBinaryIn(t, bin, dir, "recover"); err != nil {
		t.Fatalf("first recover: %v", err)
	}
	// Second invocation with no run.json must be a no-op.
	output, err := runBinaryIn(t, bin, dir, "recover")
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if !strings.Contains(output, "No run.json") {
		t.Errorf("expected 'No run.json' message, got:\n%s", output)
	}
}

// TestSpringfieldRecoverOnIdleBatchIsNoop pins that a freshly-planned batch
// (batch.json present, plans pending, no owning process) is not archived. Note
// the corrected liveness model (#10): batch.json presence alone no longer means
// "live" — the recover keys off the process flock, and with no stale running
// plan there is simply nothing to recover.
func TestSpringfieldRecoverOnIdleBatchIsNoop(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "idle-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-login"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-login", "Implement login")},
	}
	envJSON, _ := json.MarshalIndent(env, "", "  ")
	if _, err := planWithPRD(t, bin, dir, string(envJSON)); err != nil {
		t.Fatalf("plan: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "recover")
	if err != nil {
		t.Fatalf("recover on idle batch: %v\n%s", err, output)
	}
	if !strings.Contains(output, "nothing to recover") {
		t.Errorf("expected no-op message, got:\n%s", output)
	}
	// Archive still empty — we didn't archive an idle batch.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	if entries, _ := os.ReadDir(archiveDir); len(entries) != 0 {
		t.Errorf("expected 0 archives for idle batch, got %d", len(entries))
	}
}

// TestSpringfieldRecoverDetectsProcessDeadRunning pins the dogfood #10 fix: a
// batch whose owning process crashed leaves batch.json behind with a plan still
// marked running and no live flock holder. recover must detect the orphan and
// reset the stale running marker to interrupted (rather than the old "still has
// a live batch.json — nothing to recover").
func TestSpringfieldRecoverDetectsProcessDeadRunning(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "crashed-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-login"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-login", "Implement login")},
	}
	envJSON, _ := json.MarshalIndent(env, "", "  ")
	if _, err := planWithPRD(t, bin, dir, string(envJSON)); err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Simulate a crash mid-run: plan stuck running, lock file left behind with
	// a dead pid (flock probe will find no live holder).
	statePath := filepath.Join(dir, ".springfield", "execution", "state.json")
	staleState, _ := json.MarshalIndent(map[string]any{
		"plans": map[string]any{
			"plan-login": map[string]any{"status": "running", "attempts": 1},
		},
	}, "", "  ")
	if err := os.WriteFile(statePath, staleState, 0o644); err != nil {
		t.Fatalf("write stale state: %v", err)
	}
	lockPath := filepath.Join(dir, ".springfield", ".lock")
	if err := os.WriteFile(lockPath, []byte("000000999999\n2026-05-04T00:00:00Z\n"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "recover")
	if err != nil {
		t.Fatalf("recover on process-dead batch: %v\n%s", err, output)
	}
	if !strings.Contains(output, "no live springfield process") || !strings.Contains(output, "plan-login") {
		t.Errorf("expected orphan-running detection naming the plan, got:\n%s", output)
	}

	// The stale running marker must be reset to interrupted on disk.
	project, err := conductor.LoadProject(dir)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}
	if got := project.State.Plans["plan-login"].Status; got != conductor.StatusInterrupted {
		t.Errorf("plan status = %q, want interrupted", got)
	}
}

// TestSpringfieldRecoverFailsClosedOnStatPermissionError — codex finding #4:
// recover must NOT treat a non-ENOENT stat failure as orphan. A read that
// cannot complete (e.g. permission-denied) must fail closed so live state is
// never destroyed on a degraded read.
func TestSpringfieldRecoverFailsClosedOnStatPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based permission test does not apply when running as root")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "perm-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-login"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-login", "Implement login")},
	}
	envJSON, _ := json.MarshalIndent(env, "", "  ")
	if _, err := planWithPRD(t, bin, dir, string(envJSON)); err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Make the plans dir un-statable (remove execute bit on parent) so
	// stat(batch.json) returns EACCES rather than ENOENT.
	plansDir := filepath.Join(dir, ".springfield", "plans")
	if err := os.Chmod(plansDir, 0o000); err != nil {
		t.Fatalf("chmod plans dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(plansDir, 0o755) })

	output, err := runBinaryIn(t, bin, dir, "recover")
	if err == nil {
		t.Fatalf("expected recover to fail closed on permission error, got:\n%s", output)
	}
	if !strings.Contains(output, "refusing to recover") && !strings.Contains(output, "stat batch.json") {
		t.Errorf("expected stat-error message, got:\n%s", output)
	}

	// Restore perms and verify run.json is UNCHANGED (fail-closed guarantee).
	_ = os.Chmod(plansDir, 0o755)
	if _, ok, _ := batch.ReadRun(dir); !ok {
		t.Error("run.json must not be cleared on non-ENOENT recover abort")
	}
}

func TestSpringfieldRecoverDiagnoseDoesNotModifyState(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: "ghost"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".springfield", "plans", "ghost", "evidence", "01"), 0o755); err != nil {
		t.Fatalf("MkdirAll evidence dir: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "recover", "--diagnose")
	if err != nil {
		t.Fatalf("recover --diagnose: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Diagnosis:") {
		t.Errorf("expected Diagnosis header, got:\n%s", output)
	}
	if !strings.Contains(output, "evidence dir:") || !strings.Contains(output, filepath.Join(dir, ".springfield", "plans", "ghost", "evidence", "01")) {
		t.Errorf("expected evidence dir details, got:\n%s", output)
	}
	// State untouched.
	if _, ok, _ := batch.ReadRun(dir); !ok {
		t.Error("--diagnose must not clear run.json")
	}
}

// TestSpringfieldRecoverDiagnoseShowsPlansRegistered confirms the orphan
// diagnose output surfaces plan-level progress derived from conductor state
// even when batch.json is missing. Replaces the deleted active_phase_idx
// signal with something the operator can actually act on.
func TestSpringfieldRecoverDiagnoseShowsPlansRegistered(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Orphan: run.json points at a batch with no batch.json.
	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: "ghost"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	// Register two plan units, mark one integrated, one pending.
	writePlanFileBinary(t, dir, ".springfield/plans", "alpha", "# alpha")
	writePlanFileBinary(t, dir, ".springfield/plans", "beta", "# beta")
	writeConductorConfigBinary(t, dir, &conductor.Config{
		PlansDir:                   ".springfield/plans",
		WorktreeBase:               ".worktrees",
		MaxRetries:                 2,
		SingleWorkstreamIterations: 50,
		SingleWorkstreamTimeout:    3600,
		Tool:                       "claude",
		PlanUnits: []conductor.PlanUnit{
			{ID: "alpha", Title: "Alpha", Path: ".springfield/plans/alpha.md", Order: 1},
			{ID: "beta", Title: "Beta", Path: ".springfield/plans/beta.md", Order: 2},
		},
	})
	writeConductorStateBinary(t, dir, &conductor.State{
		Plans: map[string]*conductor.PlanState{
			"alpha": {
				Status: conductor.StatusCompleted,
				Merge: &conductor.MergeOutcome{
					Status:           conductor.MergeSucceeded,
					SourceSyncStatus: "synced",
				},
				Cleanup: &conductor.CleanupOutcome{Status: conductor.CleanupSucceeded},
			},
			// beta: no entry → pending
		},
	})

	output, err := runBinaryIn(t, bin, dir, "recover", "--diagnose")
	if err != nil {
		t.Fatalf("recover --diagnose: %v\n%s", err, output)
	}
	if !strings.Contains(output, "plans registered: 2 (integrated 1, running 0)") {
		t.Fatalf("expected plan-level progress line in diagnose output:\n%s", output)
	}
}

// TestSpringfieldRecoverDiagnoseSurfacesUnavailableStateLoad confirms the
// orphan diagnose dump explicitly reports when conductor state cannot be
// loaded, rather than silently omitting the line (which would be misleading
// in a forensics context).
func TestSpringfieldRecoverDiagnoseSurfacesUnavailableStateLoad(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Orphan: run.json points at a batch with no batch.json.
	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: "ghost"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}
	// Corrupt execution config so LoadProjectRaw fails.
	corrupt := filepath.Join(dir, ".springfield", "execution", "config.json")
	if err := os.MkdirAll(filepath.Dir(corrupt), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt config: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "recover", "--diagnose")
	if err != nil {
		t.Fatalf("recover --diagnose should not abort on state load failure: %v\n%s", err, output)
	}
	if !strings.Contains(output, "plans registered: (unavailable") {
		t.Fatalf("expected explicit unavailable line for state load failure:\n%s", output)
	}
}

func TestSpringfieldStatusDegradesOnMissingBatchJSON(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: "ghost"}); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status should degrade gracefully, got err=%v\n%s", err, output)
	}
	if !strings.Contains(output, "orphaned") {
		t.Errorf("expected 'orphaned' in degraded status, got:\n%s", output)
	}
	if !strings.Contains(output, "springfield recover") {
		t.Errorf("expected 'springfield recover' hint, got:\n%s", output)
	}
}
