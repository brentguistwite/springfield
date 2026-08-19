package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
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

	// Match ANY concrete US-NNN id inside a <story-pass> marker, not just the
	// two original offenders (US-001, US-099). Without this generalization a
	// future edit introducing e.g. <story-pass>US-042</story-pass> would sail
	// through and recreate the same hallucination footgun.
	concretePassMarker := regexp.MustCompile(`<story-pass>US-\d+</story-pass>`)
	if matches := concretePassMarker.FindAllString(content, -1); len(matches) > 0 {
		t.Errorf("docs/prd-format.md contains %d copyable marker literal(s) %v; "+
			"all <story-pass> examples in this doc must use abstract US-NNN placeholder notation, "+
			"never a concrete numeric id", len(matches), matches)
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

// TestNoSequentialBatchFraming pins spg-9: the shipped execution model is
// phase-ordered — parallel phases run their plans concurrently in
// per-plan-branches mode, and only consolidate mode is sequential (see
// docs/prd-format.md phases table). The top-level docs once framed the whole
// batch as "sequential", which drifted from the shipped batchexec parallel
// path. Guard README.md and AGENTS.md so the stale one-liner can't creep back.
//
// CLAUDE.md is a symlink to AGENTS.md, so guarding AGENTS.md covers both.
// Asserted across both surfaces so a partial rewrite is also caught — same
// pattern as the tests above.
func TestNoSequentialBatchFraming(t *testing.T) {
	root := repoRoot(t)
	surfaces := []string{
		filepath.Join(root, "README.md"),
		filepath.Join(root, "AGENTS.md"),
	}

	for _, path := range surfaces {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "sequential batch") {
			t.Errorf("%s contains stale \"sequential batch\" framing; the shipped model is phase-ordered "+
				"(parallel phases concurrent in per-plan-branches mode, only consolidate mode sequential)", path)
		}
	}
}
