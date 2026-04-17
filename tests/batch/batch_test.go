package batch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"springfield/internal/features/batch"
)

// --- sanitize ---

func TestSanitizeID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello World", "hello-world"},
		{"Feat: Add OAuth 2.0", "feat-add-oauth-2-0"},
		{"  --leading-trailing--  ", "leading-trailing"},
		{"UPPER_CASE", "upper-case"},
		{"a", "a"},
		{"", ""},
	}
	for _, c := range cases {
		got := batch.SanitizeID(c.in)
		// collapse consecutive dashes that our regex already handles
		if got != c.want {
			t.Errorf("SanitizeID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUniqueID(t *testing.T) {
	existing := map[string]struct{}{"foo": {}, "foo-2": {}}
	if got := batch.UniqueID("foo", existing); got != "foo-3" {
		t.Errorf("UniqueID = %q, want foo-3", got)
	}
	if got := batch.UniqueID("bar", existing); got != "bar" {
		t.Errorf("UniqueID = %q, want bar", got)
	}
}

// --- compile ---

func TestCompilePromptMode(t *testing.T) {
	out, err := batch.Compile(batch.CompileInput{
		Title:  "add oauth",
		Source: "Implement OAuth 2.0 login.",
		Kind:   batch.SourcePrompt,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID != "add-oauth" {
		t.Errorf("batch ID = %q, want add-oauth", out.Batch.ID)
	}
	if out.Batch.IntegrationMode != batch.IntegrationBatch {
		t.Errorf("integration mode = %q, want batch", out.Batch.IntegrationMode)
	}
	if len(out.Batch.Slices) != 1 {
		t.Fatalf("slice count = %d, want 1", len(out.Batch.Slices))
	}
	if out.Batch.Slices[0].Status != batch.SliceQueued {
		t.Errorf("slice status = %q, want queued", out.Batch.Slices[0].Status)
	}
}

func TestCompileFileMode_ParsesTasks(t *testing.T) {
	plan := `# Springfield Plan

## Task 1: Scaffold the repo
Set up directory structure.

## Task 2: Add API
Implement REST endpoints.

## Task 3: UI layer
Build the frontend.
`
	out, err := batch.Compile(batch.CompileInput{
		Title:  "scaffold",
		Source: plan,
		Kind:   batch.SourceFile,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Batch.Slices) != 3 {
		t.Fatalf("slice count = %d, want 3", len(out.Batch.Slices))
	}
	if out.Batch.Slices[0].ID != "01" {
		t.Errorf("slice[0].ID = %q, want 01", out.Batch.Slices[0].ID)
	}
	if out.Batch.Slices[1].Title != "Add API" {
		t.Errorf("slice[1].Title = %q, want 'Add API'", out.Batch.Slices[1].Title)
	}
}

func TestCompileEmptySourceReturnsError(t *testing.T) {
	_, err := batch.Compile(batch.CompileInput{Source: "  ", Kind: batch.SourcePrompt})
	if err == nil {
		t.Fatal("expected error for empty source")
	}
}

func TestCompileDeduplicatesID(t *testing.T) {
	existing := map[string]struct{}{"scaffold": {}}
	out, err := batch.Compile(batch.CompileInput{
		Title:       "scaffold",
		Source:      "do stuff",
		Kind:        batch.SourcePrompt,
		ExistingIDs: existing,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID != "scaffold-2" {
		t.Errorf("batch ID = %q, want scaffold-2", out.Batch.ID)
	}
}

// --- storage ---

func TestWriteAndReadBatch(t *testing.T) {
	dir := t.TempDir()
	paths, err := batch.NewPaths(dir, "my-batch")
	if err != nil {
		t.Fatalf("NewPaths: %v", err)
	}

	b := batch.Batch{
		ID:              "my-batch",
		Title:           "My Batch",
		SourceKind:      batch.SourcePrompt,
		IntegrationMode: batch.IntegrationBatch,
		Phases:          []batch.Phase{{Mode: batch.PhaseSerial, Slices: []string{"01"}}},
		Slices:          []batch.Slice{{ID: "01", Title: "Do stuff", Status: batch.SliceQueued}},
	}

	if err := batch.WriteBatch(paths, b, "do stuff"); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	got, err := batch.ReadBatch(paths)
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}
	if got.ID != "my-batch" {
		t.Errorf("batch ID = %q, want my-batch", got.ID)
	}
	if len(got.Slices) != 1 {
		t.Fatalf("slice count = %d, want 1", len(got.Slices))
	}

	sourcePath := paths.SourcePath()
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(data) != "do stuff" {
		t.Errorf("source = %q, want 'do stuff'", string(data))
	}
}

func TestWriteAndReadRun(t *testing.T) {
	dir := t.TempDir()

	r := batch.Run{
		ActiveBatchID:  "my-batch",
		ActivePhaseIdx: 0,
		LastCheckpoint: time.Now().UTC().Truncate(time.Second),
	}
	if err := batch.WriteRun(dir, r); err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	got, ok, err := batch.ReadRun(dir)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if !ok {
		t.Fatal("ReadRun: expected ok=true")
	}
	if got.ActiveBatchID != "my-batch" {
		t.Errorf("ActiveBatchID = %q, want my-batch", got.ActiveBatchID)
	}
}

func TestReadRunMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, ok, err := batch.ReadRun(dir)
	if err != nil {
		t.Fatalf("ReadRun: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing run.json")
	}
}

func TestArchiveBatch(t *testing.T) {
	dir := t.TempDir()
	paths, _ := batch.NewPaths(dir, "my-batch")
	b := batch.Batch{
		ID:    "my-batch",
		Title: "My Batch",
		Slices: []batch.Slice{
			{ID: "01", Title: "Do stuff", Status: batch.SliceDone},
		},
	}
	if err := batch.WriteBatch(paths, b, "source"); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	if err := batch.ArchiveBatch(dir, b, "replaced"); err != nil {
		t.Fatalf("ArchiveBatch: %v", err)
	}

	// Plan dir should be gone.
	if _, err := os.Stat(paths.PlanDir()); !os.IsNotExist(err) {
		t.Error("plan dir should be removed after archive")
	}

	// Archive entry should exist.
	archiveDir := batch.ArchiveDir(dir)
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected archive entry to exist")
	}
}

// --- legacy detection ---

func writeLegacyIndex(t *testing.T, dir, activeID string, ids []string) {
	t.Helper()
	workDir := filepath.Join(dir, ".springfield", "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	works := "["
	for i, id := range ids {
		if i > 0 {
			works += ","
		}
		works += `{"id":"` + id + `","title":"` + strings.Title(id) + `"}`
	}
	works += "]"

	activeField := ""
	if activeID != "" {
		activeField = `"active_work_id":"` + activeID + `",`
	}

	content := `{` + activeField + `"works":` + works + `}`
	if err := os.WriteFile(filepath.Join(workDir, "index.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
}

func TestDetectLegacyWork_NoState(t *testing.T) {
	dir := t.TempDir()
	summary, err := batch.DetectLegacyWork(dir)
	if err != nil {
		t.Fatalf("DetectLegacyWork: %v", err)
	}
	if summary != nil {
		t.Errorf("expected nil summary, got %+v", summary)
	}
}

func TestDetectLegacyWork_WithActiveID(t *testing.T) {
	dir := t.TempDir()
	writeLegacyIndex(t, dir, "wave-c2", []string{"wave-c1", "wave-c2"})

	summary, err := batch.DetectLegacyWork(dir)
	if err != nil {
		t.Fatalf("DetectLegacyWork: %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.ID != "wave-c2" {
		t.Errorf("ID = %q, want wave-c2", summary.ID)
	}
}

func TestDetectLegacyWork_FallsBackToLast(t *testing.T) {
	dir := t.TempDir()
	writeLegacyIndex(t, dir, "", []string{"alpha", "beta", "gamma"})

	summary, err := batch.DetectLegacyWork(dir)
	if err != nil {
		t.Fatalf("DetectLegacyWork: %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.ID != "gamma" {
		t.Errorf("ID = %q, want gamma", summary.ID)
	}
}
