package planmerge

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Git is the minimal git surface planmerge requires. The interface lets
// tests substitute a scripted implementation without shelling out.
type Git interface {
	// ResolveRef returns the SHA pointed to by ref in dir.
	ResolveRef(dir, ref string) (string, error)
	// Head returns the SHA at HEAD inside the working tree at dir.
	Head(dir string) (string, error)
	// CurrentBranch returns the local branch name HEAD points at in dir,
	// or an error when dir is in detached HEAD.
	CurrentBranch(dir string) (string, error)
	// WorktreeAddDetached creates a new worktree at path in detached HEAD
	// state pointing at ref. The new worktree is registered under dir.
	WorktreeAddDetached(dir, path, ref string) error
	// WorktreeRemoveForce removes the registered worktree at path. The
	// --force option ensures dirty checkouts are still cleaned up.
	WorktreeRemoveForce(dir, path string) error
	// MergeFFOnly fast-forwards HEAD inside dir to branch. Fails when the
	// fast-forward is not possible.
	MergeFFOnly(dir, branch string) error
	// UpdateBranchRef performs an atomic CAS update of refs/heads/<branch>
	// in dir from expected to newSHA.
	UpdateBranchRef(dir, branch, newSHA, expected string) error
	// ResetHard resets HEAD in dir to sha, syncing index and working tree.
	// Used to advance the source checkout's worktree after a successful
	// update-ref when the target branch is the source's current HEAD.
	ResetHard(dir, sha string) error
	// BranchDelete deletes the branch in dir, even when not merged into the
	// current branch (the merge target was advanced via UpdateBranchRef so
	// the branch is fully reachable).
	BranchDelete(dir, branch string) error
}

// CLIGit shells out to the system git binary.
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

// ResolveRef returns the resolved SHA for ref relative to dir.
func (g CLIGit) ResolveRef(dir, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("ref must not be empty")
	}
	return g.run(dir, "rev-parse", "--verify", ref)
}

// Head returns the SHA at HEAD in the working tree at dir.
func (g CLIGit) Head(dir string) (string, error) {
	return g.run(dir, "rev-parse", "HEAD")
}

// CurrentBranch returns the local branch name HEAD points at in dir.
func (g CLIGit) CurrentBranch(dir string) (string, error) {
	out, err := g.run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if out == "HEAD" {
		return "", fmt.Errorf("repo at %s is in detached HEAD", dir)
	}
	return out, nil
}

// ResetHard runs `git reset --hard <sha>` inside dir, advancing HEAD,
// index, and working tree atomically. Springfield invokes this only after
// a slice-2 clean-source preflight has held since execution began, so the
// destructive nature is bounded by the same invariant the slice already
// relies on.
func (g CLIGit) ResetHard(dir, sha string) error {
	if sha == "" {
		return fmt.Errorf("sha must not be empty")
	}
	_, err := g.run(dir, "reset", "--hard", sha)
	return err
}

// WorktreeAddDetached registers a new worktree at path with detached HEAD
// pointing at ref.
func (g CLIGit) WorktreeAddDetached(dir, path, ref string) error {
	_, err := g.run(dir, "worktree", "add", "--detach", path, ref)
	return err
}

// WorktreeRemoveForce removes the registered worktree at path with --force
// so a dirty checkout cannot block cleanup.
func (g CLIGit) WorktreeRemoveForce(dir, path string) error {
	_, err := g.run(dir, "worktree", "remove", "--force", path)
	return err
}

// MergeFFOnly fast-forwards the current HEAD in dir to branch, failing if
// the merge is not a fast-forward.
func (g CLIGit) MergeFFOnly(dir, branch string) error {
	_, err := g.run(dir, "merge", "--ff-only", branch)
	return err
}

// UpdateBranchRef performs `git update-ref refs/heads/<branch> <newSHA>
// <expected>`, the atomic CAS used by Integrate to publish the fast-forward
// to the source-checkout branch ref without checking it out.
func (g CLIGit) UpdateBranchRef(dir, branch, newSHA, expected string) error {
	if branch == "" {
		return fmt.Errorf("branch must not be empty")
	}
	full := normalizeBranchRef(branch)
	_, err := g.run(dir, "update-ref", full, newSHA, expected)
	return err
}

// BranchDelete deletes the local branch in dir using -D so an unmerged
// branch (from git's view of the current source-checkout HEAD) can still be
// removed once Integrate has confirmed the merge target ref points at the
// merged head.
func (g CLIGit) BranchDelete(dir, branch string) error {
	_, err := g.run(dir, "branch", "-D", branch)
	return err
}

// normalizeBranchRef returns the fully qualified ref for a branch name. A
// caller may pass either "main" or "refs/heads/main"; both normalize to
// "refs/heads/main".
func normalizeBranchRef(branch string) string {
	if strings.HasPrefix(branch, "refs/heads/") {
		return branch
	}
	return "refs/heads/" + branch
}
