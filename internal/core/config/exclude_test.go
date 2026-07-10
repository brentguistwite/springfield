package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInit creates a committed git repo rooted at dir so worktrees can be added.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "init")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestEnsureSpringfieldExcludeCreatesFileInRepo(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	added, err := EnsureSpringfieldExclude(dir)
	if err != nil {
		t.Fatalf("EnsureSpringfieldExclude: %v", err)
	}
	if !added {
		t.Fatal("added = false, want true on first write")
	}

	body := readExclude(t, filepath.Join(dir, ".git"))
	if !strings.Contains(body, springfieldExcludeMarker) {
		t.Fatalf("exclude missing marker; got:\n%s", body)
	}
	for _, pat := range []string{".springfield/", "springfield.local.toml"} {
		if !strings.Contains(body, pat) {
			t.Fatalf("exclude missing pattern %q; got:\n%s", pat, body)
		}
	}
}

// TestEnsureSpringfieldExcludeFromLinkedWorktree pins the core team-safe
// guarantee: called from a linked worktree, the block must land in the MAIN
// checkout's shared info/exclude, not the worktree's throwaway git dir.
func TestEnsureSpringfieldExcludeFromLinkedWorktree(t *testing.T) {
	main := t.TempDir()
	gitInit(t, main)

	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, main, "worktree", "add", "-b", "feat", wt)

	added, err := EnsureSpringfieldExclude(wt)
	if err != nil {
		t.Fatalf("ensureSpringfieldExclude from worktree: %v", err)
	}
	if !added {
		t.Fatal("added = false, want true on first write")
	}

	// Block lands in the main checkout's shared exclude.
	body := readExclude(t, filepath.Join(main, ".git"))
	if !strings.Contains(body, springfieldExcludeMarker) {
		t.Fatalf("main info/exclude missing block; got:\n%s", body)
	}

	// And NOT in a per-worktree exclude, which would defeat sharing.
	worktreeGitDir := worktreeGitDir(t, wt)
	if _, err := os.Stat(filepath.Join(worktreeGitDir, "info", "exclude")); err == nil {
		t.Fatalf("unexpected per-worktree exclude at %s", worktreeGitDir)
	}
}

func TestEnsureSpringfieldExcludeIdempotent(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	if _, err := EnsureSpringfieldExclude(dir); err != nil {
		t.Fatalf("first EnsureSpringfieldExclude: %v", err)
	}
	added, err := EnsureSpringfieldExclude(dir)
	if err != nil {
		t.Fatalf("second EnsureSpringfieldExclude: %v", err)
	}
	if added {
		t.Fatal("added = true on second write, want false (idempotent)")
	}

	body := readExclude(t, filepath.Join(dir, ".git"))
	if n := strings.Count(body, springfieldExcludeMarker); n != 1 {
		t.Fatalf("marker count = %d, want exactly 1", n)
	}
}

// TestEnsureSpringfieldExcludePreservesExistingNoTrailingNewline verifies the
// block is separated from pre-existing content that lacks a trailing newline,
// rather than being glued onto the last line.
func TestEnsureSpringfieldExcludePreservesExistingNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)

	infoDir := filepath.Join(dir, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	excludePath := filepath.Join(infoDir, "exclude")
	if err := os.WriteFile(excludePath, []byte("*.log"), 0o644); err != nil { // no trailing newline
		t.Fatal(err)
	}

	if _, err := EnsureSpringfieldExclude(dir); err != nil {
		t.Fatalf("EnsureSpringfieldExclude: %v", err)
	}

	body := readExclude(t, filepath.Join(dir, ".git"))
	if strings.Contains(body, "*.log"+springfieldExcludeMarker) {
		t.Fatalf("block glued onto prior line; got:\n%s", body)
	}
	if !strings.Contains(body, "*.log\n") {
		t.Fatalf("prior content not preserved on its own line; got:\n%s", body)
	}
	if !strings.Contains(body, springfieldExcludeMarker) {
		t.Fatalf("marker missing; got:\n%s", body)
	}
}

func readExclude(t *testing.T, gitDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(gitDir, "info", "exclude"))
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	return string(b)
}

// worktreeGitDir resolves the per-worktree git directory (the `.git` file in a
// linked worktree points here) so a test can assert nothing was written there.
func worktreeGitDir(t *testing.T, wt string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", wt, "rev-parse", "--absolute-git-dir")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse --absolute-git-dir: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}
