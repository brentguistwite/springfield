package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
	"springfield/internal/features/planner"
	"springfield/internal/features/workflow"
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

func TestSpringfieldStartFailsWithLegacyAndMentionsResume(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Write legacy state but no new batch.
	writeLegacyWorkIndex(t, dir, "wave-c2", []string{"wave-c2"})

	output, err := runBinaryIn(t, bin, dir, "start")
	if err == nil {
		t.Fatalf("expected start to fail with legacy-only state, got:\n%s", output)
	}
	if !strings.Contains(output, "legacy") {
		t.Fatalf("expected error to mention legacy state, got:\n%s", output)
	}
	if !strings.Contains(output, "springfield plan") {
		t.Fatalf("expected error to mention springfield plan, got:\n%s", output)
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
		"Integration: batch",
		"Slices:",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected status output to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldStatusFallsBackToLegacy(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Write legacy work state (no new batch).
	writeApprovedWorkflowDraft(t, dir, planner.SplitSingle)

	output, err := runBinaryIn(t, bin, dir, "status", "--work", "wave-c2")
	if err != nil {
		t.Fatalf("status with legacy work failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Work: wave-c2") {
		t.Fatalf("expected legacy work in status output, got:\n%s", output)
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

func TestSpringfieldMigrateLegacyToBatch(t *testing.T) {
	dir := t.TempDir()

	// Set up legacy work state.
	writeLegacyWorkState(t, dir, "wave-c2", "Unified execution surface")

	summary := batch.LegacyWorkSummary{
		ID:       "wave-c2",
		Title:    "Unified execution surface",
		Status:   "completed",
		Approved: true,
	}
	b, err := batch.MigrateLegacyToBatch(dir, summary)
	if err != nil {
		t.Fatalf("MigrateLegacyToBatch: %v", err)
	}

	if b.ID == "" {
		t.Error("migrated batch ID should not be empty")
	}

	// run.json should be written.
	run, ok, err := batch.ReadRun(dir)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if !ok {
		t.Fatal("expected run.json after migration")
	}
	if run.ActiveBatchID != b.ID {
		t.Errorf("run.ActiveBatchID = %q, want %q", run.ActiveBatchID, b.ID)
	}

	// Legacy work dir should be archived, not deleted.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	hasLegacy := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "wave-c2") {
			hasLegacy = true
		}
	}
	if !hasLegacy {
		t.Error("expected legacy work dir to be archived (renamed), not deleted")
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

// --- helpers ---

func writeLegacyWorkIndex(t *testing.T, dir, activeID string, ids []string) {
	t.Helper()
	workDir := filepath.Join(dir, ".springfield", "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	type entry struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	type index struct {
		ActiveWorkID string  `json:"active_work_id,omitempty"`
		Works        []entry `json:"works"`
	}

	works := make([]entry, 0, len(ids))
	for _, id := range ids {
		works = append(works, entry{ID: id, Title: id})
	}
	idx := index{ActiveWorkID: activeID, Works: works}
	data, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(workDir, "index.json"), data, 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
}

func writeLegacyWorkState(t *testing.T, dir, workID, title string) {
	t.Helper()

	if err := workflow.WriteDraft(dir, workflow.Draft{
		RequestBody: title,
		Response: planner.Response{
			Mode:    planner.ModeDraft,
			WorkID:  workID,
			Title:   title,
			Summary: title,
			Split:   planner.SplitSingle,
			Workstreams: []planner.Workstream{
				{Name: "01", Title: title},
			},
		},
	}); err != nil {
		t.Fatalf("WriteDraft: %v", err)
	}
}
