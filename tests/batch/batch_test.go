package batch_test

import (
	"os"
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
		ID:         "my-batch",
		Title:      "My Batch",
		SourceKind: batch.SourcePrompt,
		Phases:     []batch.Phase{{Mode: batch.PhaseSerial, Slices: []string{"01"}}},
		Slices:     []batch.Slice{{ID: "01", Title: "Do stuff", Status: batch.SliceQueued}},
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

func TestCompile_FileSourceCapturesTaskBody(t *testing.T) {
	source := `# My Plan

## Task 1: Build the thing

Do step A.
Do step B.

Constraints: must not break X.

## Task 2: Verify

Run the tests.
`
	out, err := batch.Compile(batch.CompileInput{
		Title:  "demo",
		Source: source,
		Kind:   batch.SourceFile,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Batch.Slices) != 2 {
		t.Fatalf("slice count = %d, want 2", len(out.Batch.Slices))
	}

	s1 := out.Batch.Slices[0]
	for _, want := range []string{"Do step A.", "Do step B.", "Constraints: must not break X."} {
		if !strings.Contains(s1.Summary, want) {
			t.Errorf("slice 1 Summary missing %q\nGot:\n%s", want, s1.Summary)
		}
	}
	if strings.Contains(s1.Summary, "## Task 2") {
		t.Errorf("slice 1 Summary leaked next-task header:\n%s", s1.Summary)
	}

	s2 := out.Batch.Slices[1]
	if !strings.Contains(s2.Summary, "Run the tests.") {
		t.Errorf("slice 2 Summary missing body, got:\n%s", s2.Summary)
	}
}

func TestCompile_NumberedSubheadingDoesNotSplitSlice(t *testing.T) {
	source := `# My Plan

## Task 1: Big task

Do the work.

### 1. Acceptance Criteria

- A
- B

### 2. Notes

Some prose.

## Task 2: Next thing

Other work.
`
	out, err := batch.Compile(batch.CompileInput{
		Title:  "demo",
		Source: source,
		Kind:   batch.SourceFile,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Batch.Slices) != 2 {
		t.Fatalf("slice count = %d, want 2 (numbered subheadings must not split); slices: %+v", len(out.Batch.Slices), out.Batch.Slices)
	}
	if !strings.Contains(out.Batch.Slices[0].Summary, "Acceptance Criteria") {
		t.Errorf("slice 1 Summary should contain its subheading body, got:\n%s", out.Batch.Slices[0].Summary)
	}
}

func TestRunBatchSerialOnly(t *testing.T) {
	// Verify that a batch with no explicit parallel phases runs all slices in PhaseSerial.
	out, err := batch.Compile(batch.CompileInput{
		Title:  "serial batch",
		Source: "# Plan\n## Task 1: First\n## Task 2: Second\n",
		Kind:   batch.SourceFile,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Batch.Phases) != 1 {
		t.Fatalf("expected 1 phase (serial), got %d", len(out.Batch.Phases))
	}
	if out.Batch.Phases[0].Mode != batch.PhaseSerial {
		t.Errorf("phase mode = %q, want serial", out.Batch.Phases[0].Mode)
	}
	if len(out.Batch.Phases[0].Slices) != 2 {
		t.Errorf("phase slice count = %d, want 2", len(out.Batch.Phases[0].Slices))
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

