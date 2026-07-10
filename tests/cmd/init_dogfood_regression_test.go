package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"springfield/internal/core/gitstatus"
)

// TestInitDefaultLeavesCleanTreeForPreflight is the US-008 dogfood regression:
// it reproduces the exact failure team-safe init was built to fix. Before the
// fix, `springfield init` in a repo left springfield.toml and .springfield/ as
// untracked files, so the very next `springfield start` refused with
// preflight-dirty-source.
//
// The test drives the real init binary in default (team-safe) mode against a
// freshly-committed checkout, then asserts the four properties that together
// prove the fix:
//
//  1. the tracked .gitignore is byte-for-byte unchanged (a repo the operator
//     does not own is never modified),
//  2. .git/info/exclude carries the team-safe block,
//  3. springfield.toml is present, and
//  4. the preflight dirty-source check passes on the clean checkout.
//
// (4) runs the *exact* computation the preflight uses — `git status
// --porcelain` fed through [gitstatus.Dirty], the same predicate
// planrun.CLIGit.IsDirty calls — so a regression in either the info/exclude
// writer or the owned-path predicate re-breaks this test.
func TestInitDefaultLeavesCleanTreeForPreflight(t *testing.T) {
	bin := buildBinary(t)
	dir := initRealGitRepo(t)

	// The dogfood scenario is a clean *existing* checkout, not a pile of
	// untracked files. Commit the seed so the only post-init delta is
	// Springfield's own artifacts.
	gitMust(t, dir, "add", "-A")
	gitMust(t, dir, "commit", "-m", "seed")
	if out := gitOut(t, dir, "status", "--porcelain"); out != "" {
		t.Fatalf("seed checkout not clean before init:\n%s", out)
	}

	gitignore := filepath.Join(dir, ".gitignore")
	before, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("read seeded .gitignore: %v", err)
	}

	out, err := runBinaryIn(t, bin, dir, "init", "--agents", "claude")
	if err != nil {
		t.Fatalf("init (default mode): %v\n%s", err, out)
	}

	// 1. Tracked .gitignore untouched, byte-for-byte.
	after, err := os.ReadFile(gitignore)
	if err != nil {
		t.Fatalf("read .gitignore after init: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("default mode mutated tracked .gitignore\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}

	// 2. The team-safe block landed in info/exclude and covers the config file.
	exclude, err := os.ReadFile(excludePath(dir))
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	for _, pat := range []string{".springfield/", "springfield.toml", "springfield.local.toml"} {
		if !strings.Contains(string(exclude), pat) {
			t.Fatalf("expected %q in info/exclude, got:\n%s", pat, exclude)
		}
	}

	// 3. springfield.toml is present (init created the project config).
	if _, err := os.Stat(filepath.Join(dir, "springfield.toml")); err != nil {
		t.Fatalf("springfield.toml missing after init: %v", err)
	}

	// 4. The preflight dirty-source check passes on the clean checkout. This is
	// the exact check planrun.CLIGit.IsDirty runs. With info/exclude covering
	// them, git never even lists Springfield's artifacts, so the raw porcelain
	// is empty too — assert that first, then the predicate as the backstop.
	porcelain := gitOut(t, dir, "status", "--porcelain")
	if porcelain != "" {
		t.Errorf("expected clean tree after init (info/exclude should hide Springfield artifacts), got:\n%s", porcelain)
	}
	if gitstatus.Dirty(porcelain) {
		t.Fatalf("preflight dirty-source check would REFUSE after default init; porcelain:\n%s", porcelain)
	}
}
