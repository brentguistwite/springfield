package cmd_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

// installTamperingAgent writes a fake agent script that runs an arbitrary
// shell snippet (the "tamper" step) before exiting zero. Used by the B
// predicate tests to simulate an agent that corrupts Springfield state.
func installTamperingAgent(t *testing.T, binDir, name, tamperCmd string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	script := fmt.Sprintf("#!/bin/sh\n%s\necho 'agent-output'\n", tamperCmd)
	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write tampering agent: %v", err)
	}
}

// TestTamperRule1_AgentDeletesBatchJSON reproduces the squibby incident:
// agent removes .springfield/plans/<id>/batch.json mid-run. Tamper must
// fire, snapshot must restore, forensics sidecar must land. The batch
// stays active (not archived) so the user can retry without recompiling.
func TestTamperRule1_AgentDeletesBatchJSON(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	fakeBinDir := filepath.Join(dir, "bin")
	installTamperingAgent(t, fakeBinDir, "claude", "rm -f "+filepath.Join(dir, ".springfield", "plans")+"/*/batch.json")

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected start to fail on tampered state, got:\n%s", output)
	}
	if !strings.Contains(output, "state tampered") {
		t.Errorf("expected 'state tampered' in output, got:\n%s", output)
	}

	// Batch stays active: run.json is preserved, plan dir is restored from
	// snapshot, forensics sidecar is written.
	run, ok, _ := batch.ReadRun(dir)
	if !ok {
		t.Error("run.json should persist after tamper detection (batch stays retryable)")
	}
	if run.ActiveBatchID == "" {
		t.Error("run.json should retain ActiveBatchID after tamper")
	}

	// Forensics sidecar present.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	foundSidecar := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tamper.json") {
			foundSidecar = true
			break
		}
	}
	if !foundSidecar {
		t.Errorf("expected forensics sidecar (*.tamper.json) after tamper, got entries=%v", entries)
	}

	// Plan dir restored: batch.json exists again.
	matches, _ := filepath.Glob(filepath.Join(dir, ".springfield", "plans", "*", "batch.json"))
	if len(matches) == 0 {
		t.Error("expected batch.json restored under plans/<id>/ after tamper")
	}
}

// TestTamperRule2_AgentWritesGarbageJSON — predicate rule 2 (JSON parse) fires.
func TestTamperRule2_AgentWritesGarbageJSON(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	fakeBinDir := filepath.Join(dir, "bin")
	installTamperingAgent(t, fakeBinDir, "claude",
		"for f in "+filepath.Join(dir, ".springfield", "plans")+"/*/batch.json; do echo 'not json' > \"$f\"; done")

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected failure on garbage JSON, got:\n%s", output)
	}
	if !strings.Contains(output, "state tampered by agent") {
		t.Errorf("expected tamper message in output, got:\n%s", output)
	}
	if !strings.Contains(output, "batch.json") {
		t.Errorf("expected batch.json in tamper reason, got:\n%s", output)
	}
}

// TestTamperSliceTitleRewriteDetected — the hardened predicate is byte-equal
// across the control plane, so any mutation (not just ID shape) trips it.
// Previously an agent could rewrite phase structure, slice order, or slice
// status/title/error with the same ID set and slip through.
func TestTamperSliceTitleRewriteDetected(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	fakeBinDir := filepath.Join(dir, "bin")
	// Use `jq` if available, otherwise a brute sed over the title value.
	installTamperingAgent(t, fakeBinDir, "claude",
		"for f in "+filepath.Join(dir, ".springfield", "plans")+"/*/batch.json; do sed -i '' 's/\"title\": \"[^\"]*\"/\"title\": \"HIJACKED\"/' \"$f\" || sed -i 's/\"title\": \"[^\"]*\"/\"title\": \"HIJACKED\"/' \"$f\"; done")

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected title rewrite to be detected as tamper, got:\n%s", output)
	}
	if !strings.Contains(output, "state tampered") {
		t.Errorf("expected tamper message, got:\n%s", output)
	}
}

// TestTamperRunJSONDetected — agent rewriting run.json is caught too.
func TestTamperRunJSONDetected(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	fakeBinDir := filepath.Join(dir, "bin")
	installTamperingAgent(t, fakeBinDir, "claude",
		"echo '{\"active_batch_id\":\"bogus\",\"active_phase_idx\":0}' > "+filepath.Join(dir, ".springfield", "run.json"))

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err == nil {
		t.Fatalf("expected run.json tamper to fail start, got:\n%s", output)
	}
	if !strings.Contains(output, "run.json") {
		t.Errorf("expected run.json in tamper reason, got:\n%s", output)
	}
}

// TestTamperNoopAgentPasses — an agent that does nothing must NOT trigger the
// predicate. This guards against false-positive rewrites of the batch file.
func TestTamperNoopAgentPasses(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir + ":" + os.Getenv("PATH")}, "start")
	if err != nil {
		t.Fatalf("noop agent should succeed, got err=%v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Errorf("expected completed, got:\n%s", output)
	}
}

// TestABInteractionMatrix_RunJSONAloneDeleted covers matrix case 4:
// tampering deletes both batch.json AND run.json. Next start sees "no active
// batch" and MUST NOT write a spurious orphan archive.
func TestABInteractionMatrix_RunJSONAloneDeleted(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Do the thing"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Manually delete both files to simulate out-of-band tampering.
	if err := os.RemoveAll(filepath.Join(dir, ".springfield", "plans")); err != nil {
		t.Fatalf("rm plans: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ".springfield", "run.json")); err != nil {
		t.Fatalf("rm run.json: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "start")
	if err == nil {
		t.Fatalf("expected 'no plan' error, got:\n%s", output)
	}
	if !strings.Contains(output, "springfield plan") {
		t.Errorf("expected 'springfield plan' hint, got:\n%s", output)
	}
	// Crucial: no spurious orphan archive written.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, _ := os.ReadDir(archiveDir)
	if len(entries) != 0 {
		t.Errorf("expected 0 archive entries (no orphan to archive), got %d", len(entries))
	}
}
