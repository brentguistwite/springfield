package conductor

import "sync"

// gitOpsMu serializes mutating git operations against the shared repository
// (.git/worktrees metadata, packed-refs) across concurrently-executing plans:
// worktree add/remove and branch creation/deletion. Concurrent `git worktree
// add` invocations race on that shared metadata, so every mutating call on a
// concurrently-executed path must run inside WithGitLock. Read-only git
// commands need no lock. The lock also covers the worktree-path
// decision→reservation window (preflight prepare through running-state
// publication) so two plans whose IDs sanitize to the same worktree key
// cannot both claim the same path.
var gitOpsMu sync.Mutex

// WithGitLock runs fn while holding the process-wide git-operations lock.
// Do not call SaveState-holding code that re-enters WithGitLock (the lock is
// not reentrant); keep fn scoped to the git mutation plus the state write
// that reserves its result.
func WithGitLock(fn func()) {
	gitOpsMu.Lock()
	defer gitOpsMu.Unlock()
	fn()
}
