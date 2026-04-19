package lock_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"springfield/internal/core/lock"
)

func TestLockAcquireReleaseRoundTrip(t *testing.T) {
	root := t.TempDir()

	lk, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if err := lk.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	lk2, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("second Acquire after release: %v", err)
	}
	defer lk2.Release()
}

func TestLockConflictReturnsErrLockHeldWithPid(t *testing.T) {
	root := t.TempDir()

	lk, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lk.Release()

	_, err = lock.Acquire(root)
	if err == nil {
		t.Fatal("expected error from second Acquire, got nil")
	}

	var held *lock.ErrLockHeld
	if !errors.As(err, &held) {
		t.Fatalf("expected *lock.ErrLockHeld, got %T: %v", err, err)
	}
	if held.PID != os.Getpid() {
		t.Errorf("ErrLockHeld.PID = %d, want %d", held.PID, os.Getpid())
	}
	if held.Since.IsZero() {
		t.Error("ErrLockHeld.Since is zero")
	}
}

func TestLockConflictHandlesTornFile(t *testing.T) {
	root := t.TempDir()

	lk, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer lk.Release()

	// Truncate the lock file to empty (torn write simulation).
	lockPath := filepath.Join(root, ".springfield", ".lock")
	if err := os.WriteFile(lockPath, []byte{}, 0o600); err != nil {
		t.Fatalf("truncate lock file: %v", err)
	}

	_, err = lock.Acquire(root)
	if err == nil {
		t.Fatal("expected error from second Acquire, got nil")
	}

	var held *lock.ErrLockHeld
	if !errors.As(err, &held) {
		t.Fatalf("expected *lock.ErrLockHeld, got %T: %v", err, err)
	}
	if held.PID != 0 {
		t.Errorf("expected PID=0 for torn file, got %d", held.PID)
	}
	if !held.Since.IsZero() {
		t.Errorf("expected Since=zero for torn file, got %v", held.Since)
	}
}

// TestLockConflictHandlesMissingFile verifies that readHeld returns
// ErrLockHeld{PID:0} when the lock file is missing at read time.
// This exercises the fallback path that fires when the file disappears between
// a failed flock attempt and the subsequent pid-read (e.g. holding process
// just exited and cleaned up).
func TestLockConflictHandlesMissingFile(t *testing.T) {
	// Use ReadHeldFromPath (exported for test) to call readHeld on a path that
	// does not exist — directly testing the "file gone" branch.
	nonExistentPath := filepath.Join(t.TempDir(), "no-such-lock-file")

	result := lock.ReadHeldFromPath(nonExistentPath)
	if result == nil {
		t.Fatal("ReadHeldFromPath returned nil, want *ErrLockHeld")
	}
	if result.PID != 0 {
		t.Errorf("ErrLockHeld.PID = %d for missing file, want 0", result.PID)
	}
	if !result.Since.IsZero() {
		t.Errorf("ErrLockHeld.Since = %v for missing file, want zero", result.Since)
	}
}

func TestLockFileContainsPidAndTimestamp(t *testing.T) {
	root := t.TempDir()

	before := time.Now().Add(-time.Second)
	lk, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lk.Release()

	lockPath := filepath.Join(root, ".springfield", ".lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}

	lines := splitLines(string(data))
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines in lock file, got %d: %q", len(lines), string(data))
	}

	pidStr := lines[0]
	if pidStr != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock file line 1 = %q, want %q", pidStr, strconv.Itoa(os.Getpid()))
	}

	ts, err := time.Parse(time.RFC3339, lines[1])
	if err != nil {
		t.Fatalf("line 2 not parseable as RFC3339: %q: %v", lines[1], err)
	}
	if ts.Before(before) {
		t.Errorf("timestamp %v is before test start %v", ts, before)
	}
}

func TestLockReleaseRemovesFile(t *testing.T) {
	root := t.TempDir()

	lk, err := lock.Acquire(root)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	lockPath := filepath.Join(root, ".springfield", ".lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should exist after Acquire: %v", err)
	}

	if err := lk.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("expected lock file removed after Release, Stat err=%v", err)
	}
}

// splitLines splits s by newlines, trimming empty trailing entries.
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
