package planrun_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/features/conductor/planrun"
	"springfield/internal/features/prd"
)

// writePRD writes a PRD struct to path as indented JSON.
func writePRD(t *testing.T, path string, p prd.PRD) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readPRD(t *testing.T, path string) prd.PRD {
	t.Helper()
	p, err := prd.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile %s: %v", path, err)
	}
	return p
}

func TestMarkPassedAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.json")
	initial := prd.PRD{
		ID: "p",
		UserStories: []prd.UserStory{
			{ID: "US-001", Passes: false},
		},
	}
	writePRD(t, prdPath, initial)

	if err := planrun.MarkPassed(prdPath, "US-001"); err != nil {
		t.Fatalf("MarkPassed: %v", err)
	}

	loaded := readPRD(t, prdPath)
	if len(loaded.UserStories) != 1 || !loaded.UserStories[0].Passes {
		t.Fatalf("expected passes=true, got %+v", loaded.UserStories)
	}
	// Verify no temp files remain
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temp file %q left behind", e.Name())
		}
	}
}

func TestMarkPassedIdempotent(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.json")
	initial := prd.PRD{
		ID: "p",
		UserStories: []prd.UserStory{
			{ID: "US-001", Passes: true},
		},
	}
	writePRD(t, prdPath, initial)

	stat1, err := os.Stat(prdPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if err := planrun.MarkPassed(prdPath, "US-001"); err != nil {
		t.Fatalf("MarkPassed on already-passed: %v", err)
	}

	stat2, err := os.Stat(prdPath)
	if err != nil {
		t.Fatalf("stat2: %v", err)
	}
	if !stat2.ModTime().Equal(stat1.ModTime()) {
		t.Fatal("file was rewritten for idempotent call (mtime changed)")
	}
}

func TestMarkPassedUnknownStoryIDReturnsError(t *testing.T) {
	dir := t.TempDir()
	prdPath := filepath.Join(dir, "prd.json")
	initial := prd.PRD{
		ID: "p",
		UserStories: []prd.UserStory{
			{ID: "US-001", Passes: false},
		},
	}
	writePRD(t, prdPath, initial)

	err := planrun.MarkPassed(prdPath, "US-099")
	if err == nil {
		t.Fatal("expected error for unknown story ID")
	}
	if !strings.Contains(err.Error(), "US-099") {
		t.Fatalf("error should mention the unknown ID, got: %v", err)
	}
}

func TestAppendProgressHappyPath(t *testing.T) {
	dir := t.TempDir()
	progressPath := filepath.Join(dir, "progress.md")

	if err := planrun.AppendProgress(progressPath, "line one"); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	}
	if err := planrun.AppendProgress(progressPath, "line two"); err != nil {
		t.Fatalf("AppendProgress: %v", err)
	}

	data, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "line one\n") {
		t.Fatalf("expected 'line one' in %q", content)
	}
	if !strings.Contains(content, "line two\n") {
		t.Fatalf("expected 'line two' in %q", content)
	}
}

func TestAppendProgressReadOnlyReturnsError(t *testing.T) {
	dir := t.TempDir()
	progressPath := filepath.Join(dir, "progress.md")
	// Create the file and make it read-only.
	if err := os.WriteFile(progressPath, []byte("existing\n"), 0o444); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := planrun.AppendProgress(progressPath, "new line")
	if err == nil {
		t.Fatal("expected error writing to read-only file")
	}
}
