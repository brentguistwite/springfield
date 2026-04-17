package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/batch"
)

func TestSpringfieldPlanHelp(t *testing.T) {
	output, err := runSpringfield(t, "plan", "--help")
	if err != nil {
		t.Fatalf("plan --help failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Compile a Springfield plan from a markdown file or prompt into a runnable batch.",
		"--file",
		"--prompt",
		"--replace",
		"--append",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected plan help to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldPlanFromPrompt(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Implement OAuth 2.0 login")
	if err != nil {
		t.Fatalf("springfield plan --prompt failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Batch:",
		"Title:",
		"Slices: 1",
		`Run "springfield start" to execute.`,
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected plan output to contain %q, got:\n%s", marker, output)
		}
	}

	// Verify run.json was written.
	runPath := filepath.Join(dir, ".springfield", "run.json")
	data, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatalf("read run.json: %v", err)
	}
	var run batch.Run
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatalf("decode run.json: %v", err)
	}
	if run.ActiveBatchID == "" {
		t.Error("expected active_batch_id to be set in run.json")
	}
}

func TestSpringfieldPlanFromFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	planContent := `# Add auth layer

## Task 1: Scaffold auth package
Set up directory structure.

## Task 2: Implement JWT
Add JWT signing and verification.

## Task 3: Wire middleware
Connect middleware to router.
`
	planPath := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(planPath, []byte(planContent), 0o644); err != nil {
		t.Fatalf("write plan file: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "plan", "--file", planPath)
	if err != nil {
		t.Fatalf("springfield plan --file failed: %v\n%s", err, output)
	}

	if !strings.Contains(output, "Slices: 3") {
		t.Fatalf("expected 3 slices parsed from plan file, got:\n%s", output)
	}
	for _, marker := range []string{"01", "02", "03"} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected slice id %q in output, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldPlanFilePlusPromptReturnsError(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "plan", "--file", "plan.md", "--prompt", "do stuff")
	if err == nil {
		t.Fatalf("expected error when --file and --prompt both provided, got:\n%s", output)
	}
	if !strings.Contains(output, "not both") {
		t.Fatalf("expected 'not both' error message, got:\n%s", output)
	}
}

func TestSpringfieldPlanRefusesWithActiveBatch(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Write first batch.
	_, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "first batch")
	if err != nil {
		t.Fatalf("first plan failed: %v", err)
	}

	// Second plan without --replace should fail.
	output, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "second batch")
	if err == nil {
		t.Fatalf("expected error for second plan without --replace, got:\n%s", output)
	}
	if !strings.Contains(output, "--replace") {
		t.Fatalf("expected error to mention --replace, got:\n%s", output)
	}
}

func TestSpringfieldPlanReplaceArchivesPrior(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	runBinaryIn(t, bin, dir, "plan", "--prompt", "first batch")

	output, err := runBinaryIn(t, bin, dir, "plan", "--replace", "--prompt", "second batch")
	if err != nil {
		t.Fatalf("plan --replace failed: %v\n%s", err, output)
	}

	archiveDir := filepath.Join(dir, ".springfield", "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected archive entry after --replace")
	}

	var run batch.Run
	data, _ := os.ReadFile(filepath.Join(dir, ".springfield", "run.json"))
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatalf("decode run.json: %v", err)
	}
	// Active batch should be the new one (not the first).
	for _, e := range entries {
		if strings.Contains(e.Name(), run.ActiveBatchID) {
			t.Errorf("active batch id %q should not appear in archive names", run.ActiveBatchID)
		}
	}
}

func TestSpringfieldPlanReplaceKeepsPriorWhenNewFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based write-failure test does not apply when running as root")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "first batch"); err != nil {
		t.Fatalf("first plan failed: %v", err)
	}
	firstRun, _, _ := batch.ReadRun(dir)
	firstID := firstRun.ActiveBatchID

	// Make plans/ read-only so WriteBatch for the new batch fails, but archive/
	// remains writable (so ArchiveBatch can succeed in the old code path,
	// revealing the bug: archive written before new batch confirmed).
	plansDir := filepath.Join(dir, ".springfield", "plans")
	if err := os.Chmod(plansDir, 0o500); err != nil {
		t.Fatalf("chmod plans dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(plansDir, 0o755) })

	output, err := runBinaryIn(t, bin, dir, "plan", "--replace", "--prompt", "second batch")
	if err == nil {
		t.Fatalf("expected plan --replace to fail with read-only plans dir, got:\n%s", output)
	}

	// Restore permissions so we can inspect.
	_ = os.Chmod(plansDir, 0o755)

	// Archive must remain empty — prior batch must still be active.
	archiveDir := filepath.Join(dir, ".springfield", "archive")
	if entries, _ := os.ReadDir(archiveDir); len(entries) > 0 {
		t.Errorf("archive should be empty after failed --replace, found %d entries", len(entries))
	}
	gotRun, ok, err := batch.ReadRun(dir)
	if err != nil || !ok {
		t.Fatalf("run.json should still exist: ok=%v err=%v", ok, err)
	}
	if gotRun.ActiveBatchID != firstID {
		t.Errorf("ActiveBatchID = %q, want unchanged %q", gotRun.ActiveBatchID, firstID)
	}
}

func TestSpringfieldPlanAppendDedupsSliceIDs(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	firstPlan := `# First
## Task 1: Alpha
## Task 2: Beta
`
	planPath := filepath.Join(dir, "first.md")
	if err := os.WriteFile(planPath, []byte(firstPlan), 0o644); err != nil {
		t.Fatalf("write first plan: %v", err)
	}
	if _, err := runBinaryIn(t, bin, dir, "plan", "--file", planPath); err != nil {
		t.Fatalf("first plan: %v", err)
	}

	secondPlan := `# Second
## Task 1: Gamma
## Task 2: Delta
`
	secondPath := filepath.Join(dir, "second.md")
	if err := os.WriteFile(secondPath, []byte(secondPlan), 0o644); err != nil {
		t.Fatalf("write second plan: %v", err)
	}
	if _, err := runBinaryIn(t, bin, dir, "plan", "--append", "--file", secondPath); err != nil {
		t.Fatalf("append plan: %v", err)
	}

	run, _, _ := batch.ReadRun(dir)
	paths, _ := batch.NewPaths(dir, run.ActiveBatchID)
	b, err := batch.ReadBatch(paths)
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}
	if len(b.Slices) != 4 {
		t.Fatalf("slice count = %d, want 4", len(b.Slices))
	}
	seen := map[string]int{}
	for _, s := range b.Slices {
		seen[s.ID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("slice ID %q appears %d times; want unique", id, n)
		}
	}
	// Phase slice references must match the actual slice IDs.
	phaseIDs := map[string]struct{}{}
	for _, p := range b.Phases {
		for _, id := range p.Slices {
			phaseIDs[id] = struct{}{}
		}
	}
	for _, s := range b.Slices {
		if _, ok := phaseIDs[s.ID]; !ok {
			t.Errorf("slice %q not referenced by any phase", s.ID)
		}
	}
}

func TestSpringfieldPlanUnsafeIDSanitized(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "plan", "--prompt", "Feat: Add OAuth 2.0 Login!!")
	if err != nil {
		t.Fatalf("plan failed: %v\n%s", err, output)
	}

	// Batch ID should not contain uppercase or special chars.
	data, _ := os.ReadFile(filepath.Join(dir, ".springfield", "run.json"))
	var run batch.Run
	json.Unmarshal(data, &run)

	for _, ch := range run.ActiveBatchID {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			t.Errorf("batch ID %q contains invalid char %q", run.ActiveBatchID, string(ch))
		}
	}
}
