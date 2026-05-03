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

func (g CLIGit) IsDirty(dir string) (bool, error) {
	out, err := g.run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
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
