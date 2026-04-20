package wakelock

import (
	"os/exec"
	"testing"
)

func TestAcquire_Darwin_LaunchesCaffeinateWithCorrectArgs(t *testing.T) {
	var gotArgs []string
	newCmd := func(name string, args ...string) *exec.Cmd {
		gotArgs = append([]string{name}, args...)
		return exec.Command("true")
	}

	release, err := acquire(newCmd, "darwin", 99999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	release()

	want := []string{"caffeinate", "-i", "-s", "-w", "99999"}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	for i, v := range want {
		if gotArgs[i] != v {
			t.Errorf("arg[%d] = %q, want %q", i, gotArgs[i], v)
		}
	}
}

func TestAcquire_NonDarwin_NoOp(t *testing.T) {
	called := false
	newCmd := func(name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command("true")
	}

	release, err := acquire(newCmd, "linux", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("newCmd should not be called on non-darwin")
	}
	release() // must not panic
}

func TestAcquire_Darwin_CaffeinateNotFound_ReturnsError(t *testing.T) {
	newCmd := func(name string, args ...string) *exec.Cmd {
		return exec.Command("__no_such_binary_xyzzy__")
	}

	release, err := acquire(newCmd, "darwin", 1)
	if err == nil {
		t.Fatal("expected error when caffeinate not found")
	}
	release() // safe no-op on error path
}

func TestAcquire_Darwin_ReleaseStopsProcess(t *testing.T) {
	newCmd := func(name string, args ...string) *exec.Cmd {
		return exec.Command("sleep", "100")
	}

	release, err := acquire(newCmd, "darwin", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	release()
	// second call must not panic (idempotent kill)
	release()
}
