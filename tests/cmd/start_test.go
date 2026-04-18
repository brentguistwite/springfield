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
		"Execute the active Springfield batch for the current project from its saved progress.",
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
	_, err := singleSlicePlan(t, bin, dir, "Implement login")
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
	_, planErr := singleSlicePlan(t, bin, dir, "Implement login flow")
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

// TestSpringfieldStartRecoversFromPostArchiveCrash verifies the Workstream A
// invariant: on success the archive is written first, then run.json is cleared.
// If the process dies after archive + before clear, run.json points at an
// already-archived batch id; the next springfield start must recover
// idempotently (archive already exists → skip, clear cursor, exit 0).
func TestSpringfieldStartRecoversFromPostArchiveCrash(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Implement login flow"); err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	fakeBinDir := filepath.Join(dir, "bin")
	argvPath := filepath.Join(dir, "claude.argv")
	installFakeAgentBinary(t, fakeBinDir, "claude", argvPath)

	// Run to completion normally.
	if _, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir}, "start"); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	// Confirm normal completion state: archive present, no run.json.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected archive entry after completion, got err=%v entries=%d", err, len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, ".springfield", "run.json")); !os.IsNotExist(err) {
		t.Fatalf("expected run.json cleared after completion, err=%v", err)
	}

	// Simulate "crash between archive and ClearRun": restore a run.json
	// pointing at the archived batch id. Archive filenames are stable:
	// <batchID>.json (single archive per id — see writeJSONExclusive).
	archivedID := ""
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			archivedID = strings.TrimSuffix(name, ".json")
			break
		}
	}
	if archivedID == "" {
		t.Fatalf("could not extract batch id from archive entries: %v", entries)
	}
	if err := batch.WriteRun(dir, batch.Run{ActiveBatchID: archivedID}); err != nil {
		t.Fatalf("restore ghost run.json: %v", err)
	}

	// Next start: expect orphan recovery path (exits 0, clears run.json).
	output, err := runBinaryInWithEnv(t, bin, dir, []string{"PATH=" + fakeBinDir}, "start")
	if err != nil {
		t.Fatalf("expected orphan recovery to exit 0, got err=%v\n%s", err, output)
	}
	if !strings.Contains(output, "orphaned") {
		t.Errorf("expected orphan message in output, got:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".springfield", "run.json")); !os.IsNotExist(statErr) {
		t.Errorf("expected run.json cleared after orphan recovery, got err=%v", statErr)
	}
}

func TestSpringfieldStartCompletionWarnsWhenArchiveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based write-failure test does not apply when running as root")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "Implement login flow"); err != nil {
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
