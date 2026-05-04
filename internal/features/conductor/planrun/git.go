package planrun

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Git is the minimal git surface planrun needs. The interface lets tests
// substitute an in-memory or scripted implementation without shelling out.
type Git interface {
	IsRepo(dir string) (bool, error)
	IsDirty(dir string) (bool, error)
	ResolveRef(dir, ref string) (string, error)
	CurrentBranch(dir string) (string, error)
	BranchExists(dir, branch string) (bool, error)
	WorktreeListPaths(dir string) ([]string, error)
	WorktreeAddNewBranch(dir, path, branch, base string) error
	WorktreeAddExistingBranch(dir, path, branch string) error
	// Head returns the SHA at HEAD inside dir. Used by the runner to
	// stamp PlanHead on the post-execution state record so the on-disk
	// state contains the SHA the slice promises to record even if the
	// process dies before merge integration runs.
	Head(dir string) (string, error)
}

// CLIGit shells out to the system git binary. dir is the git repo root used
// for `-C` so callers do not have to chdir.
type CLIGit struct{}

func (CLIGit) run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (g CLIGit) IsRepo(dir string) (bool, error) {
	out, err := g.run(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		// Distinguish "not a repo" from "git missing": rev-parse outside a
		// repo prints to stderr; treat any failure as "not a repo" so the
		// caller can produce a single actionable error.
		return false, nil
	}
	return out == "true", nil
}

// dirtyIgnoredPrefixes lists path prefixes whose presence in `git status`
// output is Springfield's own bookkeeping, not user-visible dirt. The log
// file `springfield start` writes lives under `.springfield/logs/`, and the
// worktree base under `.worktrees/`, so neither must dirty the source
// preflight even when the user has not added them to `.gitignore`.
var dirtyIgnoredPrefixes = []string{".springfield/", ".worktrees/"}

func (g CLIGit) IsDirty(dir string) (bool, error) {
	out, err := g.run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if out == "" {
		return false, nil
	}
	for _, line := range strings.Split(out, "\n") {
		if !lineIsSpringfieldOwned(line) {
			return true, nil
		}
	}
	return false, nil
}

// lineIsSpringfieldOwned returns true when a `git status --porcelain` line
// describes a path under one of the Springfield-owned prefixes. Porcelain
// format is "XY <path>" (or "XY <orig> -> <new>" for renames); we strip the
// status code and any rename arrow before checking the prefix.
func lineIsSpringfieldOwned(line string) bool {
	if len(line) < 4 {
		return false
	}
	rest := line[3:]
	if idx := strings.Index(rest, " -> "); idx >= 0 {
		rest = rest[idx+len(" -> "):]
	}
	rest = strings.TrimPrefix(rest, "\"")
	rest = strings.TrimSuffix(rest, "\"")
	for _, p := range dirtyIgnoredPrefixes {
		if strings.HasPrefix(rest, p) {
			return true
		}
	}
	return false
}

func (g CLIGit) ResolveRef(dir, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("ref must not be empty")
	}
	return g.run(dir, "rev-parse", ref)
}

func (g CLIGit) CurrentBranch(dir string) (string, error) {
	out, err := g.run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if out == "HEAD" {
		return "", fmt.Errorf("repo is in detached HEAD; pass an explicit plan ref")
	}
	return out, nil
}

func (g CLIGit) BranchExists(dir, branch string) (bool, error) {
	_, err := g.run(dir, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (g CLIGit) WorktreeListPaths(dir string) ([]string, error) {
	out, err := g.run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimSpace(strings.TrimPrefix(line, "worktree ")))
		}
	}
	return paths, nil
}

func (g CLIGit) WorktreeAddNewBranch(dir, path, branch, base string) error {
	_, err := g.run(dir, "worktree", "add", "-b", branch, path, base)
	return err
}

func (g CLIGit) WorktreeAddExistingBranch(dir, path, branch string) error {
	_, err := g.run(dir, "worktree", "add", path, branch)
	return err
}

// Head returns the SHA at HEAD inside dir.
func (g CLIGit) Head(dir string) (string, error) {
	return g.run(dir, "rev-parse", "HEAD")
}
