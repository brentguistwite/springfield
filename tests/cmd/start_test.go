package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

func TestSpringfieldStartHelp(t *testing.T) {
	output, err := runSpringfield(t, "start", "--help")
	if err != nil {
		t.Fatalf("start --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Execute the active Springfield batch from its saved cursor.",
		"springfield plan",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected start help to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldStartFailsWithNoBatch(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "start")
	if err == nil {
		t.Fatalf("expected start to fail with no batch, got:\n%s", output)
	}
	if !strings.Contains(output, "springfield plan") {
		t.Fatalf("expected error to mention 'springfield plan', got:\n%s", output)
	}
}

func TestSpringfieldStatusShowsBatchState(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Create a batch via plan command.
	_, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Implement login")
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "status")
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Batch:",
		"Title:",
		"Slices:",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected status output to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldStatusNoStateReturnsError(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "status")
	if err == nil {
		t.Fatalf("expected status to fail with no state, got:\n%s", output)
	}
	if !strings.Contains(output, "springfield plan") {
		t.Fatalf("expected error to mention 'springfield plan', got:\n%s", output)
	}
}

func TestSpringfieldStartRunsBatchSlices(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Compile a batch first.
	_, planErr := runBinaryIn(t, bin, dir, "plan", "--prompt", "Implement login flow")
	if planErr != nil {
		t.Fatalf("plan failed: %v", planErr)
	}

	// Install fake claude binary.
	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	output, err := runBinaryInWithEnv(
		t, bin, dir,
		[]string{"PATH=" + fakeBinDir},
		"start",
	)
	if err != nil {
		t.Fatalf("springfield start failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Fatalf("expected completed status, got:\n%s", output)
	}

	// run.json should be cleared after completion.
	runPath := filepath.Join(dir, ".springfield", "run.json")
	if _, err := os.Stat(runPath); !os.IsNotExist(err) {
		t.Error("expected run.json to be cleared after completion")
	}

	// Archive should contain the completed batch.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected archive entry after completed batch")
	}
}

func TestSpringfieldStartCompletionDoesNotStrandCursor(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based write-failure test does not apply when running as root")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Implement login flow"); err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	run, ok, err := batch.ReadRun(dir)
	if err != nil || !ok || run.ActiveBatchID == "" {
		t.Fatalf("ReadRun: ok=%v err=%v id=%q", ok, err, run.ActiveBatchID)
	}
	activeBatchID := run.ActiveBatchID

	// Pre-create archive/ so ArchiveBatch's MkdirAll wouldn't be the failing call under chmod.
	if err := os.MkdirAll(filepath.Join(dir, ".springfield", "archive"), 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	sfDir := filepath.Join(dir, ".springfield")
	if err := os.Chmod(sfDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sfDir, 0o755) })

	output, err := runBinaryInWithEnv(
		t, bin, dir,
		[]string{"PATH=" + fakeBinDir},
		"start",
	)
	if err == nil {
		t.Fatalf("expected start to fail when ClearRun fails, got:\n%s", output)
	}

	_ = os.Chmod(sfDir, 0o755)

	// Invariant: plan dir must still exist (NOT archived prematurely).
	planDir := filepath.Join(dir, ".springfield", "plans", activeBatchID)
	if _, statErr := os.Stat(planDir); os.IsNotExist(statErr) {
		t.Errorf("plan dir %s was archived before ClearRun succeeded — cursor is now stranded", planDir)
	}
	// Cursor still points at the batch (not corrupted).
	gotRun, ok, _ := batch.ReadRun(dir)
	if !ok || gotRun.ActiveBatchID != activeBatchID {
		t.Errorf("run.json should still point at %q, got ok=%v active=%q", activeBatchID, ok, gotRun.ActiveBatchID)
	}
}

func TestSpringfieldStartCompletionWarnsWhenArchiveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based write-failure test does not apply when running as root")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Implement login flow"); err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	// Force ArchiveBatch's MkdirAll to fail by creating a non-directory at .springfield/archive.
	archivePath := filepath.Join(dir, ".springfield", "archive")
	if err := os.MkdirAll(filepath.Join(dir, ".springfield"), 0o755); err != nil {
		t.Fatalf("mkdir .springfield: %v", err)
	}
	if err := os.WriteFile(archivePath, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("create archive collision: %v", err)
	}

	output, err := runBinaryInWithEnv(
		t, bin, dir,
		[]string{"PATH=" + fakeBinDir},
		"start",
	)
	if err != nil {
		t.Fatalf("expected start to succeed (archive is best-effort), got err=%v\n%s", err, output)
	}
	if !strings.Contains(output, "Status: completed") {
		t.Errorf("expected Status: completed in output, got:\n%s", output)
	}
	if !strings.Contains(output, "warning: archive") {
		t.Errorf("expected archive warning in output, got:\n%s", output)
	}
	// Cursor was cleared (run.json gone) — that's the success signal.
	if _, ok, _ := batch.ReadRun(dir); ok {
		t.Errorf("run.json should be cleared after successful completion")
	}
}

