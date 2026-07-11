package planrun

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"springfield/internal/core/gitstatus"
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
	// Diff returns `git diff <baseRef>...HEAD` run inside dir — the plan
	// branch's net changes since it diverged from baseRef. Used to feed the
	// reviewer the work under review.
	Diff(dir, baseRef string) (string, error)
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

// IsDirty reports whether dir has uncommitted changes outside the paths
// Springfield owns. Classification of the porcelain output is delegated to
// [gitstatus.Dirty] so this gate, planmerge's resync gate, and autobranch
// share one notion of "clean".
func (g CLIGit) IsDirty(dir string) (bool, error) {
	out, err := g.run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return gitstatus.Dirty(out), nil
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

// Diff returns the net change between baseRef and HEAD using the
// symmetric-difference form `git diff baseRef...HEAD`.
func (g CLIGit) Diff(dir, baseRef string) (string, error) {
	return g.run(dir, "diff", baseRef+"...HEAD")
}

// CreateBranch creates branch at startPoint without switching the worktree
// (`git branch <branch> <startPoint>`). Used by the auto-branch flow in
// cmd/start so the operator keeps working on their original branch while the
// batch accumulates merges onto the auto-branch ref.
func (g CLIGit) CreateBranch(dir, branch, startPoint string) error {
	_, err := g.run(dir, "branch", branch, startPoint)
	return err
}
