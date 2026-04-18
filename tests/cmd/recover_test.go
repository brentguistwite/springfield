package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

func TestSpringfieldRecoverHelp(t *testing.T) {
	output, err := runSpringfield(t, "recover", "--help")
	if err != nil {
		t.Fatalf("recover --help: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Archive an orphaned batch") {
		t.Errorf("expected orphan wording in help, got:\n%s", output)
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

func TestSpringfieldRecoverOnLiveBatchIsNoop(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Compile a real batch — batch.json is live.
	if _, err := singleSlicePlan(t, bin, dir, "Implement login"); err != nil {
		t.Fatalf("plan: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "recover")
	if err != nil {
		t.Fatalf("recover on live batch: %v\n%s", err, output)
	}
	if !strings.Contains(output, "still has a live batch.json") {
		t.Errorf("expected live-batch message, got:\n%s", output)
	}
	// Archive still empty — we didn't archive a live batch.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	if entries, _ := os.ReadDir(archiveDir); len(entries) != 0 {
		t.Errorf("expected 0 archives for live batch, got %d", len(entries))
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
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Implement login"); err != nil {
		t.Fatalf("plan: %v", err)
	}
	// Make the plan dir un-statable (remove execute bit on parent) so
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

	output, err := runBinaryIn(t, bin, dir, "recover", "--diagnose")
	if err != nil {
		t.Fatalf("recover --diagnose: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Diagnosis:") {
		t.Errorf("expected Diagnosis header, got:\n%s", output)
	}
	// State untouched.
	if _, ok, _ := batch.ReadRun(dir); !ok {
		t.Error("--diagnose must not clear run.json")
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
