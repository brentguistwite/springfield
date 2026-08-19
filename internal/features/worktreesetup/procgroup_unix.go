//go:build unix

package worktreesetup

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// setProcGroup puts the command in its own process group so a timeout can kill
// the whole group — the shell plus every child it spawned — rather than just
// the shell (which would orphan a slow installer's subprocesses).
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup SIGKILLs the command's process group. With Setpgid the group id
// equals the shell's pid, so the negative pid addresses the whole group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
}
