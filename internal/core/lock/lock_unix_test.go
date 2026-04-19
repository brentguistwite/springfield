//go:build !windows

package lock_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"springfield/internal/core/lock"
)

// lockHelperMain is the subprocess entrypoint when
// LOCK_TEST_HELPER_ROOT is set. It acquires the lock, writes "ready\n" to
// stdout, then sleeps. The parent kills it with SIGKILL.
func init() {
	root := os.Getenv("LOCK_TEST_HELPER_ROOT")
	if root == "" {
		return
	}
	_, err := lock.Acquire(root)
	if err != nil {
		os.Exit(1)
	}
	os.Stdout.WriteString("ready\n")
	time.Sleep(60 * time.Second)
	os.Exit(0)
}

// repoRootForLockTest finds the module root relative to this test file.
func repoRootForLockTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile is …/<repoRoot>/internal/core/lock/lock_unix_test.go
	// repo root is three directory levels above the lock/ dir.
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
}

// TestLockKernelReleaseOnProcessDeath forks a subprocess that acquires the
// lock, kills it with SIGKILL, and verifies the parent can immediately
// re-acquire (kernel flock is released on process death).
func TestLockKernelReleaseOnProcessDeath(t *testing.T) {
	root := t.TempDir()
	repoRoot := repoRootForLockTest(t)

	// Build the test binary for this package so we can re-exec it as a helper.
	testBin := filepath.Join(t.TempDir(), "lock.test")
	buildCmd := exec.Command("go", "test", "-c", "-o", testBin, "springfield/internal/core/lock")
	buildCmd.Dir = repoRoot
	buildCmd.Env = os.Environ()
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build test binary: %v\n%s", err, out)
	}

	// Launch the test binary as a helper subprocess.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	cmd := exec.Command(testBin, "-test.run=^$") // run no tests; init() handles the work
	cmd.Stdout = pw
	cmd.Env = append(os.Environ(), "LOCK_TEST_HELPER_ROOT="+root)
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		t.Fatalf("start helper: %v", err)
	}
	pw.Close()

	// Wait for "ready".
	readyCh := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 16)
		n, _ := pr.Read(buf)
		pr.Close()
		if n > 0 {
			readyCh <- struct{}{}
		}
	}()
	select {
	case <-readyCh:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("helper did not signal ready in time")
	}

	// Kill — no graceful cleanup.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	cmd.Wait()

	// Kernel releases flock on process death; parent should acquire quickly.
	deadline := time.Now().Add(2 * time.Second)
	var acquireErr error
	for time.Now().Before(deadline) {
		var lk *lock.Lock
		lk, acquireErr = lock.Acquire(root)
		if acquireErr == nil {
			lk.Release()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("parent could not acquire lock after subprocess death: %v", acquireErr)
}
