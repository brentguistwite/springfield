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

// TestPlanSurfacesRequireExplicitFilePaths pins C2: both the agent-facing
// commands/plan.md and the human-facing skills/plan/SKILL.md must carry the
// "documentation AC names an explicit target file" constraint. A future edit
// that softens or drops the rule would let the dogfood ~75-turn-thrash failure
// mode (agent hunting for a doc file the criterion never named) recur.
//
// Asserted against both surfaces so a partial edit (one source updated, the
// other left behind) is also caught — same pattern as the off-target marker
// test above.
func TestPlanSurfacesRequireExplicitFilePaths(t *testing.T) {
	root := repoRoot(t)
	surfaces := []string{
		filepath.Join(root, "commands", "plan.md"),
		filepath.Join(root, "skills", "plan", "SKILL.md"),
	}

	// Distinguishing phrase from the C2 constraint block. Checking for both
	// the heading and an example token (`path/to/file.md`) catches a partial
	// rewrite that keeps the heading but loses the operational example.
	required := []string{
		"documentation acceptance criteria must name an explicit target file",
		"path/to/file.md",
	}

	for _, path := range surfaces {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(data)
		for _, phrase := range required {
			if !strings.Contains(content, phrase) {
				t.Errorf("%s missing required C2 phrase %q (explicit-file-targets constraint must remain documented on every plan surface)",
					path, phrase)
			}
		}
	}
}
