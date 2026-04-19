// Package lock provides a single-writer exclusive lock on the Springfield
// control-plane root directory using an OS-level flock(2). At most one
// springfield process can hold the lock at a time; the kernel automatically
// releases the lock when the holding process exits (by any means), so stale
// locks are never an issue.
package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Lock represents a held flock on .springfield/.lock. Release must be called
// to unlock and close the file descriptor. The lock file itself is never
// removed — keeping a stable inode prevents a TOCTOU race where two processes
// could each acquire an "exclusive" flock on different inodes of the same path.
type Lock struct {
	f    *os.File
	path string
}

// ErrLockHeld is returned by Acquire when another process holds the lock.
// PID and Since are zero/zero when the lock file could not be read (torn file
// or file vanished between the failed flock attempt and the read).
type ErrLockHeld struct {
	PID   int
	Since time.Time
}

func (e *ErrLockHeld) Error() string {
	if e.PID == 0 {
		return "lock held by unknown process (file missing or empty)"
	}
	return fmt.Sprintf("lock held by pid %d since %s", e.PID, e.Since.Format(time.RFC3339))
}

// lockPath returns the canonical path for the lock file inside root.
func lockPath(root string) string {
	return filepath.Join(root, ".springfield", ".lock")
}

// Acquire obtains an exclusive non-blocking flock on <root>/.springfield/.lock.
// On success it writes the current pid and timestamp into the file and returns
// the held Lock. On conflict it returns *ErrLockHeld populated from the file
// contents (or zero-valued when the file is unreadable).
//
// Symlink hardening: Acquire rejects symlinks at both the .springfield directory
// and the .lock file path. A symlinked control-plane path would let the lock
// silently operate on an external inode, breaking isolation guarantees and
// potentially overwriting unrelated files.
//
// TOCTOU: after a successful flock we verify that the path still points to the
// same inode we locked (another process may have unlinked+recreated the file
// between our open and flock). If the inode changed we unlock and return
// *ErrLockHeld{} — the stale holder's identity is unknowable.
func Acquire(root string) (*Lock, error) {
	dir := filepath.Join(root, ".springfield")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create .springfield dir: %w", err)
	}

	// Reject .springfield if it resolved to a symlink (MkdirAll follows links).
	dirStat, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat .springfield dir: %w", err)
	}
	if dirStat.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf(".springfield is a symlink; remove it before running springfield start")
	}

	path := lockPath(root)
	// O_NOFOLLOW causes ELOOP if .lock itself is a symlink, rejecting symlink injection.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	// Belt-and-suspenders: verify the open fd is a regular file. Catches devices
	// or named pipes that slipped through O_NOFOLLOW on unusual filesystems.
	fdStat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat lock file fd: %w", err)
	}
	if !fdStat.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf(".springfield/.lock is not a regular file (mode %s); remove it before running springfield start", fdStat.Mode())
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		// Another process holds the lock. Try to read pid+ts from the file.
		held := readHeld(path)
		return nil, held
	}

	// TOCTOU check: verify the file at path is still the same inode we opened.
	// If it was deleted (and possibly recreated) between open and flock, our
	// flock is on an orphaned inode and the real holder is unidentifiable.
	fStat, err := f.Stat()
	if err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, &ErrLockHeld{}
	}
	pathStat, err := os.Stat(path)
	if err != nil {
		// File gone — flock is on a deleted inode; real holder unknown.
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, &ErrLockHeld{}
	}
	if !os.SameFile(fStat, pathStat) {
		// Different inode — another process recreated the file.
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, &ErrLockHeld{}
	}

	// Write pid and timestamp in a single call to minimize torn-read window.
	pid := os.Getpid()
	ts := time.Now().UTC().Format(time.RFC3339)
	content := strconv.Itoa(pid) + "\n" + ts + "\n"

	// Truncate to avoid stale content from a previous holder.
	if err := f.Truncate(0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("truncate lock file: %w", err)
	}
	if _, err := f.WriteAt([]byte(content), 0); err != nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
		return nil, fmt.Errorf("write lock file: %w", err)
	}

	return &Lock{f: f, path: path}, nil
}

// Release unlocks the flock and closes the file descriptor. The lock file is
// intentionally NOT removed: removing it after unlock opens an inode-split race
// where a second process acquires the old inode while a third creates a new one,
// leaving two "exclusive" holders at once. Keeping the inode permanent collapses
// all contenders onto a single inode, preserving the kernel's exclusion guarantee.
func (l *Lock) Release() error {
	var firstErr error
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		firstErr = fmt.Errorf("unlock flock: %w", err)
	}
	if err := l.f.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close lock file: %w", err)
	}
	return firstErr
}

// readHeld reads the pid+timestamp from path and returns *ErrLockHeld.
// On any read/parse failure returns *ErrLockHeld{PID:0} — no panic, no other error type.
func readHeld(path string) *ErrLockHeld {
	data, err := os.ReadFile(path)
	if err != nil {
		// File gone between failed flock and this read.
		return &ErrLockHeld{}
	}
	content := strings.TrimRight(string(data), "\n")
	parts := strings.SplitN(content, "\n", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		// Torn file or empty.
		return &ErrLockHeld{}
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil {
		return &ErrLockHeld{}
	}
	ts, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return &ErrLockHeld{}
	}
	return &ErrLockHeld{PID: pid, Since: ts}
}
