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
		"Compile a Springfield plan from a caller-provided slice payload.",
		"--slices",
		"--replace",
		"--append",
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected plan help to contain %q, got:\n%s", marker, output)
		}
	}
}

func TestSpringfieldPlanFromSlicesStdin(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := planWithSlices(t, bin, dir, "Implement OAuth 2.0 login",
		"Implement OAuth 2.0 login",
		[]batch.SliceRequest{
			{ID: "01", Title: "scaffold auth package", Summary: "Set up dir structure."},
			{ID: "02", Title: "add JWT signing", Summary: "Sign + verify tokens."},
			{ID: "03", Title: "wire middleware", Summary: "Hook into router."},
		})
	if err != nil {
		t.Fatalf("plan --slices failed: %v\n%s", err, output)
	}

	for _, marker := range []string{
		"Batch:",
		"Title: Implement OAuth 2.0 login",
		"Slices: 3",
		`Run "springfield start" to execute.`,
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("expected plan output to contain %q, got:\n%s", marker, output)
		}
	}

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

	paths, _ := batch.NewPaths(dir, run.ActiveBatchID)
	b, err := batch.ReadBatch(paths)
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}
	if len(b.Slices) != 3 {
		t.Fatalf("slices persisted = %d, want 3", len(b.Slices))
	}
	if b.Slices[1].Title != "add JWT signing" {
		t.Fatalf("slice[1].Title = %q", b.Slices[1].Title)
	}
}

func TestSpringfieldPlanFromSlicesFile(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	payload := batch.SlicePayload{
		Title:  "auth layer",
		Source: "auth layer",
		Slices: []batch.SliceRequest{{ID: "01", Title: "only slice"}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payloadPath := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(payloadPath, data, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	output, err := runBinaryIn(t, bin, dir, "plan", "--slices", payloadPath)
	if err != nil {
		t.Fatalf("plan --slices <file> failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "Slices: 1") {
		t.Fatalf("expected Slices: 1, got:\n%s", output)
	}
}

func TestSpringfieldPlanRequiresSlicesFlag(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	output, err := runBinaryIn(t, bin, dir, "plan")
	if err == nil {
		t.Fatalf("expected error when --slices missing, got:\n%s", output)
	}
	if !strings.Contains(output, "--slices is required") {
		t.Fatalf("expected '--slices is required' error, got:\n%s", output)
	}
}

func TestSpringfieldPlanRejectsInvalidPayload(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	// Empty slices array.
	output, err := runBinaryInWithInput(t, bin, dir,
		`{"title":"x","source":"y","slices":[]}`,
		"plan", "--slices", "-")
	if err == nil {
		t.Fatalf("expected error for empty slices, got:\n%s", output)
	}
	if !strings.Contains(output, "at least one slice") {
		t.Fatalf("expected 'at least one slice' error, got:\n%s", output)
	}
}

func TestSpringfieldPlanRefusesWithActiveBatch(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	writeSpringfieldConfig(t, dir, "claude")

	if _, err := singleSlicePlan(t, bin, dir, "first batch"); err != nil {
		t.Fatalf("first plan failed: %v", err)
	}

	output, err := singleSlicePlan(t, bin, dir, "second batch")
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

	if _, err := singleSlicePlan(t, bin, dir, "first batch"); err != nil {
		t.Fatalf("first plan failed: %v", err)
	}

	output, err := singleSlicePlan(t, bin, dir, "second batch", "--replace")
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

	if _, err := singleSlicePlan(t, bin, dir, "first batch"); err != nil {
		t.Fatalf("first plan failed: %v", err)
	}
	firstRun, _, _ := batch.ReadRun(dir)
	firstID := firstRun.ActiveBatchID

	plansDir := filepath.Join(dir, ".springfield", "plans")
	if err := os.Chmod(plansDir, 0o500); err != nil {
		t.Fatalf("chmod plans dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(plansDir, 0o755) })

	output, err := singleSlicePlan(t, bin, dir, "second batch", "--replace")
	if err == nil {
		t.Fatalf("expected plan --replace to fail with read-only plans dir, got:\n%s", output)
	}

	_ = os.Chmod(plansDir, 0o755)

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

	first := []batch.SliceRequest{
		{ID: "01", Title: "Alpha"},
		{ID: "02", Title: "Beta"},
	}
	if _, err := planWithSlices(t, bin, dir, "first", "first plan", first); err != nil {
		t.Fatalf("first plan: %v", err)
	}

	second := []batch.SliceRequest{
		{ID: "01", Title: "Gamma"},
		{ID: "02", Title: "Delta"},
	}
	if _, err := planWithSlices(t, bin, dir, "second", "second plan", second, "--append"); err != nil {
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

	output, err := singleSlicePlan(t, bin, dir, "Feat: Add OAuth 2.0 Login!!")
	if err != nil {
		t.Fatalf("plan failed: %v\n%s", err, output)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".springfield", "run.json"))
	var run batch.Run
	json.Unmarshal(data, &run)

	for _, ch := range run.ActiveBatchID {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			t.Errorf("batch ID %q contains invalid char %q", run.ActiveBatchID, string(ch))
		}
	}
}
