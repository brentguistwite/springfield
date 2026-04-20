// Package wakelock prevents idle/system sleep while a long-running batch runs.
// On macOS it spawns caffeinate; on other platforms it is a no-op.
package wakelock

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// Acquire starts sleep prevention for the current process. Call the returned
// release when the batch ends. On non-darwin platforms release is a no-op.
// If caffeinate is unavailable, Acquire returns a non-nil error and a safe
// no-op release — the caller should log a warning and continue.
func Acquire() (release func(), err error) {
	return acquire(exec.Command, runtime.GOOS, os.Getpid())
}

func acquire(newCmd func(string, ...string) *exec.Cmd, goos string, pid int) (func(), error) {
	if goos != "darwin" {
		return func() {}, nil
	}
	cmd := newCmd("caffeinate", "-i", "-s", "-w", strconv.Itoa(pid))
	if err := cmd.Start(); err != nil {
		return func() {}, fmt.Errorf("caffeinate unavailable: %w", err)
	}
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}, nil
}
