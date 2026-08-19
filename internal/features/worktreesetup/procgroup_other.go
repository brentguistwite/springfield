//go:build !unix

package worktreesetup

import "os/exec"

// setProcGroup is a no-op on platforms without POSIX process groups.
func setProcGroup(*exec.Cmd) {}

// killGroup falls back to killing only the launched process, since there is no
// portable process-group primitive here. Springfield targets unix (darwin and
// linux); this exists so `go build ./...` stays green on other platforms.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
