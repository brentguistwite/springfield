package batch_test

import (
	"os"
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

func TestCompile_BuildsBatchFromSlices(t *testing.T) {
	out, err := batch.Compile(batch.CompileInput{
		Title:  "add oauth",
		Source: "Implement OAuth 2.0 login.",
		Slices: []batch.SliceRequest{
			{ID: "01", Title: "scaffold", Summary: "set up package"},
			{ID: "02", Title: "wire endpoint", Summary: "hook router"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID != "add-oauth" {
		t.Errorf("batch ID = %q, want add-oauth", out.Batch.ID)
	}
	if len(out.Batch.Slices) != 2 {
		t.Fatalf("slice count = %d, want 2", len(out.Batch.Slices))
	}
	if out.Batch.Slices[0].Status != batch.SliceQueued {
		t.Errorf("slice status = %q, want queued", out.Batch.Slices[0].Status)
	}
	if out.Batch.Slices[1].Summary != "hook router" {
		t.Errorf("slice[1] Summary = %q", out.Batch.Slices[1].Summary)
	}
	if len(out.Batch.Phases) != 1 || out.Batch.Phases[0].Mode != batch.PhaseSerial {
		t.Errorf("phases wrong: %+v", out.Batch.Phases)
	}
	if out.Source != "Implement OAuth 2.0 login." {
		t.Errorf("source not preserved: %q", out.Source)
	}
}

func TestCompileEmptySourceReturnsError(t *testing.T) {
	_, err := batch.Compile(batch.CompileInput{
		Title:  "x",
		Source: "  ",
		Slices: []batch.SliceRequest{{ID: "01", Title: "a"}},
	})
	if err == nil {
		t.Fatal("expected error for empty source")
	}
}

func TestCompileMissingTitleReturnsError(t *testing.T) {
	_, err := batch.Compile(batch.CompileInput{
		Title:  "",
		Source: "x",
		Slices: []batch.SliceRequest{{ID: "01", Title: "a"}},
	})
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestCompileEmptySlicesReturnsError(t *testing.T) {
	_, err := batch.Compile(batch.CompileInput{
		Title:  "x",
		Source: "y",
		Slices: nil,
	})
	if err == nil {
		t.Fatal("expected error for empty slices")
	}
}

func TestCompileDeduplicatesBatchID(t *testing.T) {
	existing := map[string]struct{}{"scaffold": {}}
	out, err := batch.Compile(batch.CompileInput{
		Title:       "scaffold",
		Source:      "do stuff",
		Slices:      []batch.SliceRequest{{ID: "01", Title: "a"}},
		ExistingIDs: existing,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if out.Batch.ID != "scaffold-2" {
		t.Errorf("batch ID = %q, want scaffold-2", out.Batch.ID)
	}
}

func TestCompileDeduplicatesSliceIDs(t *testing.T) {
	out, err := batch.Compile(batch.CompileInput{
		Title:  "demo",
		Source: "body",
		Slices: []batch.SliceRequest{
			{ID: "01", Title: "a"},
			{ID: "01", Title: "b"},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(out.Batch.Slices) != 2 {
		t.Fatalf("slice count = %d", len(out.Batch.Slices))
	}
	if out.Batch.Slices[0].ID == out.Batch.Slices[1].ID {
		t.Errorf("slice ids not deduped: %q %q", out.Batch.Slices[0].ID, out.Batch.Slices[1].ID)
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
		ID:     "my-batch",
		Title:  "My Batch",
		Phases: []batch.Phase{{Mode: batch.PhaseSerial, Slices: []string{"01"}}},
		Slices: []batch.Slice{{ID: "01", Title: "Do stuff", Status: batch.SliceQueued}},
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

func TestCompile_SerialPhaseCoversAllSlices(t *testing.T) {
	out, err := batch.Compile(batch.CompileInput{
		Title:  "serial batch",
		Source: "body",
		Slices: []batch.SliceRequest{
			{ID: "01", Title: "First"},
			{ID: "02", Title: "Second"},
		},
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

	if _, err := os.Stat(paths.PlanDir()); !os.IsNotExist(err) {
		t.Error("plan dir should be removed after archive")
	}

	archiveDir := batch.ArchiveDir(dir)
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected archive entry to exist")
	}
}
