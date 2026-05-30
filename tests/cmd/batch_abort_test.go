package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/prd"
)

// TestBatchAbortHelp verifies the subcommand exists with help text.
func TestBatchAbortHelp(t *testing.T) {
	output, err := runSpringfield(t, "batch", "abort", "--help")
	if err != nil {
		t.Fatalf("batch abort --help failed: %v\n%s", err, output)
	}
	for _, marker := range []string{"abort", "archive", "active batch"} {
		if !strings.Contains(strings.ToLower(output), marker) {
			t.Fatalf("expected batch abort help to contain %q, got:\n%s", marker, output)
		}
	}
}

// TestBatchAbortClearsActiveBatch is the happy path: an active batch is archived,
// run.json is removed, and the batch's plan units are gone from the registry,
// while an older standalone (non-batch) plan unit survives intact.
func TestBatchAbortClearsActiveBatch(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	// Compile an active batch with two plans (orders 1 and 2).
	env := prd.BatchPRDEnvelope{
		Title:  "abort-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-a", "plan-b"}}},
		Plans: []prd.BatchPRDPlan{
			minPRDPlan("plan-a", "Plan A"),
			minPRDPlan("plan-b", "Plan B"),
		},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env)); err != nil {
		t.Fatalf("compile batch: %v\n%s", err, out)
	}
	batchID := readRunJSON(t, dir).ActiveBatchID

	// Register a standalone plan unit that is NOT part of the batch, after the
	// batch's units so AddPlanUnit auto-assigns the next free order slot.
	standaloneFile := filepath.Join(dir, ".springfield", "plans", "standalone.md")
	if err := os.WriteFile(standaloneFile, []byte("# standalone plan\n"), 0o644); err != nil {
		t.Fatalf("write standalone plan file: %v", err)
	}
	if out, err := runBinaryIn(t, bin, dir, "plans", "add", "--id", "standalone-unit", "--path", "standalone.md"); err != nil {
		t.Fatalf("plans add standalone-unit: %v\n%s", err, out)
	}

	// Abort the batch.
	out, err := runBinaryIn(t, bin, dir, "batch", "abort")
	if err != nil {
		t.Fatalf("batch abort: %v\n%s", err, out)
	}

	// run.json must be gone.
	if _, statErr := os.Stat(filepath.Join(dir, ".springfield", "run.json")); !os.IsNotExist(statErr) {
		t.Errorf("run.json still present after abort (stat err = %v)", statErr)
	}

	// Archive entry must exist for the aborted batch.
	archivePath := filepath.Join(dir, ".springfield", "archive", batchID+".json")
	if _, statErr := os.Stat(archivePath); statErr != nil {
		t.Errorf("archive entry missing for aborted batch %q: %v", batchID, statErr)
	}

	// The batch's plan units must be gone; the standalone unit must survive.
	units := readPlanUnits(t, dir)
	got := make(map[string]bool, len(units))
	for _, u := range units {
		got[u.ID] = true
	}
	for _, removed := range []string{"plan-a", "plan-b"} {
		if got[removed] {
			t.Errorf("batch plan unit %q still registered after abort", removed)
		}
	}
	if !got["standalone-unit"] {
		t.Errorf("non-batch plan unit standalone-unit was removed by abort; want intact (units=%+v)", units)
	}
}

// TestBatchAbortRefusesWhenNoActiveBatch verifies a clear error when there is no
// active batch to abort.
func TestBatchAbortRefusesWhenNoActiveBatch(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	out, err := runBinaryIn(t, bin, dir, "batch", "abort")
	if err == nil {
		t.Fatalf("expected abort to fail with no active batch, got success:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "no active batch") {
		t.Fatalf("expected 'no active batch' error, got:\n%s", out)
	}
}

// TestBatchAbortRefusesWhenPlanIsRunning verifies abort is rejected when a plan
// in the batch is currently running (Status == StatusRunning in conductor state).
func TestBatchAbortRefusesWhenPlanIsRunning(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "claude")

	env := prd.BatchPRDEnvelope{
		Title:  "running-batch",
		Source: "src",
		Phases: []prd.PhasePRD{{Mode: "serial", Plans: []string{"plan-one"}}},
		Plans:  []prd.BatchPRDPlan{minPRDPlan("plan-one", "Plan One")},
	}
	if out, err := planWithPRD(t, bin, dir, buildEnvelopeJSON(t, env)); err != nil {
		t.Fatalf("compile batch: %v\n%s", err, out)
	}
	batchID := readRunJSON(t, dir).ActiveBatchID

	// Inject a running plan state so the guard fires.
	writeRunningPlanState(t, dir, "plan-one")

	out, err := runBinaryIn(t, bin, dir, "batch", "abort")
	if err == nil {
		t.Fatalf("expected abort to fail when plan is running, got success:\n%s", out)
	}
	if !strings.Contains(out, "running") {
		t.Fatalf("error should mention 'running', got:\n%s", out)
	}
	if !strings.Contains(out, "plan-one") {
		t.Fatalf("error should mention the running plan ID, got:\n%s", out)
	}

	// State must be untouched: run.json still present and pointing at the batch.
	if got := readRunJSON(t, dir).ActiveBatchID; got != batchID {
		t.Errorf("active_batch_id = %q after refused abort, want %q", got, batchID)
	}
}
