package docs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot resolves the repository root relative to this test file
// (tests/docs/format_test.go -> ../../).
func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller for repo root")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

// TestPRDFormatHasNoCopyablePassMarkers guards against off-target marker
// hallucinations (dogfood incident C1): agents that read docs/prd-format.md
// during worktree exploration were regurgitating literal example story IDs
// (US-001, US-099) verbatim into their own pass markers. The doc must explain
// the off-target rule without any literal, copy-pasteable <story-pass> marker
// bound to a concrete US-NNN id.
func TestPRDFormatHasNoCopyablePassMarkers(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs", "prd-format.md")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read docs/prd-format.md: %v", err)
	}
	content := string(data)

	forbidden := []string{
		"<story-pass>US-001</story-pass>",
		"<story-pass>US-099</story-pass>",
	}
	for _, lit := range forbidden {
		if strings.Contains(content, lit) {
			t.Errorf("docs/prd-format.md contains copyable marker literal %q; "+
				"use abstract US-NNN placeholder notation instead", lit)
		}
	}
}
