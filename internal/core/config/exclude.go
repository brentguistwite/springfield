package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// springfieldExcludeMarker delimits Springfield's managed lines inside git's
// info/exclude. ensureSpringfieldExclude keys idempotency off this marker, so a
// re-run never appends a second block.
const springfieldExcludeMarker = "# springfield: team-repo-safe ignore (git info/exclude)"

// springfieldExcludeBlock is the team-safe ignore set written to info/exclude.
// Unlike the tracked-.gitignore block it uses a plain `.springfield/` directory
// ignore (no plans/ negation): in team-safe mode nothing under .springfield/ is
// committed, so there is nothing to un-ignore.
const springfieldExcludeBlock = springfieldExcludeMarker + `
.springfield/
springfield.local.toml
`

// gitCommonDir resolves the shared git directory for dir via
// `git rev-parse --git-common-dir`. In a normal checkout this is `<dir>/.git`;
// from a *linked worktree* git returns the MAIN checkout's `.git` (an absolute
// path), so a team-safe exclude lands in the one file every worktree shares
// rather than a throwaway per-worktree git dir. git resolves a relative result
// against its -C directory, so a relative path is anchored to dir before being
// cleaned to absolute.
func gitCommonDir(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--git-common-dir")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git rev-parse --git-common-dir: %s", msg)
	}
	common := strings.TrimSpace(stdout.String())
	if common == "" {
		return "", fmt.Errorf("git rev-parse --git-common-dir: empty output")
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	return filepath.Clean(common), nil
}

// EnsureSpringfieldExclude writes the team-safe ignore block to
// <git-common-dir>/info/exclude — git's untracked per-repo ignore file. Because
// git never tracks info/exclude, ignoring Springfield's state costs a teammate
// nothing and never touches a .gitignore the operator may not own. It is the
// default ignore path for `springfield init`; the tracked-.gitignore writer is
// opt-in behind --tracked-gitignore. Idempotent: keyed off
// springfieldExcludeMarker, a second call is a no-op returning added=false.
// Creates info/ and the exclude file when absent, and inserts a separating
// newline when an existing file lacks a trailing one. Returns an error when dir
// is not inside a git repo (git-common-dir resolution fails); callers treat that
// as best-effort and warn+skip.
func EnsureSpringfieldExclude(dir string) (added bool, err error) {
	common, err := gitCommonDir(dir)
	if err != nil {
		return false, err
	}

	infoDir := filepath.Join(common, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return false, fmt.Errorf("create git info dir: %w", err)
	}
	path := filepath.Join(infoDir, "exclude")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read info/exclude: %w", err)
	}
	if bytes.Contains(data, []byte(springfieldExcludeMarker)) {
		return false, nil
	}

	var out bytes.Buffer
	out.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		out.WriteByte('\n')
	}
	out.WriteString(springfieldExcludeBlock)

	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return false, fmt.Errorf("write info/exclude: %w", err)
	}
	return true, nil
}
