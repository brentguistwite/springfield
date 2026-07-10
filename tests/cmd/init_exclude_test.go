package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// excludePath returns the info/exclude path for a normal (non-worktree) repo.
func excludePath(dir string) string {
	return filepath.Join(dir, ".git", "info", "exclude")
}

// TestInitDefaultWritesExcludeLeavesGitignoreUnchanged pins the team-repo-safe
// default: `springfield init` (no --tracked-gitignore) writes its ignore block
// to .git/info/exclude — git's untracked per-clone ignore file — and leaves the
// tracked .gitignore byte-for-byte intact, so a repo the operator does not own
// is never modified.
func TestInitDefaultWritesExcludeLeavesGitignoreUnchanged(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)

	gitignore := filepath.Join(dir, ".gitignore")
	before, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("read seeded .gitignore: %v", err)
	}

	out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude")
	if err != nil {
		t.Fatalf("init (default mode): %v\n%s", err, out)
	}

	// Tracked .gitignore must be untouched, byte-for-byte.
	after, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("read .gitignore after init: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("default mode mutated tracked .gitignore\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}

	// The team-safe block landed in info/exclude instead.
	exclude, err := os.ReadFile(excludePath(dir))
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	if !strings.Contains(string(exclude), ".springfield/") {
		t.Fatalf("expected .springfield/ in info/exclude, got:\n%s", exclude)
	}
	if !strings.Contains(out, "Added Springfield patterns to .git/info/exclude") {
		t.Errorf("expected exclude-write announcement in output, got:\n%s", out)
	}
}

// TestInitTwiceIdempotentExclude is the US-007 acceptance pin for the default
// team-safe path: re-running `springfield init` must not duplicate the
// info/exclude block or any pattern line. The three ignore writers (exclude
// block, springfield.toml, .springfield/) all run on every init, so a
// regression in any one could silently append a second copy — this locks the
// whole sequence at the binary boundary. The second run also stops announcing
// the exclude write, since EnsureSpringfieldExclude no-ops when the marker is
// already present.
func TestInitTwiceIdempotentExclude(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)

	if out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude"); err != nil {
		t.Fatalf("first init: %v\n%s", err, out)
	}
	out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude")
	if err != nil {
		t.Fatalf("second init: %v\n%s", err, out)
	}
	if strings.Contains(out, "Added Springfield patterns to .git/info/exclude") {
		t.Errorf("second init should NOT re-announce the exclude write, got:\n%s", out)
	}

	data, err := os.ReadFile(excludePath(dir))
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	body := string(data)

	if n := strings.Count(body, "# springfield: team-repo-safe ignore"); n != 1 {
		t.Fatalf("marker count = %d, want exactly 1; exclude:\n%s", n, body)
	}
	for _, pat := range []string{".springfield/", "springfield.toml", "springfield.local.toml"} {
		if n := countExactLine(body, pat); n != 1 {
			t.Errorf("pattern %q appears on %d lines, want exactly 1; exclude:\n%s", pat, n, body)
		}
	}
}

// countExactLine counts newline-delimited lines in body that equal want
// exactly. Substring counting would over-count here: "springfield.toml" is a
// prefix of "springfield.local.toml" is not, but ".springfield/" is a prefix of
// ".springfield/plans/", so an exact-line match is the only reliable duplicate
// check.
func countExactLine(body, want string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if line == want {
			n++
		}
	}
	return n
}

// TestInitTrackedGitignoreLeavesExcludeUntouched pins the opt-in path: with
// --tracked-gitignore the block is written to the tracked .gitignore and
// .git/info/exclude is never touched.
func TestInitTrackedGitignoreLeavesExcludeUntouched(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)

	out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude", "--tracked-gitignore")
	if err != nil {
		t.Fatalf("init --tracked-gitignore: %v\n%s", err, out)
	}

	// info/exclude must carry no Springfield block. A fresh `git init` may not
	// create the file at all; either the file is absent or it lacks our marker.
	if data, err := os.ReadFile(excludePath(dir)); err == nil {
		if strings.Contains(string(data), ".springfield/") {
			t.Fatalf("--tracked-gitignore must not touch info/exclude, got:\n%s", data)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat info/exclude: %v", err)
	}

	// The tracked .gitignore gained the Springfield block.
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gi), ".springfield/*") {
		t.Fatalf("expected .springfield/* in tracked .gitignore, got:\n%s", gi)
	}
	if !strings.Contains(out, "Added Springfield patterns to .gitignore") {
		t.Errorf("expected gitignore-write announcement in output, got:\n%s", out)
	}
}
