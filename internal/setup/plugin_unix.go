//go:build !windows

package setup

import (
	"os/exec"
	"syscall"
)

// inOwnProcessGroup starts claude in a process group of its own so a timed-out
// step can take down everything it started. `claude` is a shell wrapper around
// node: killing the process logos launched left the node process running,
// holding the pipes this package is still reading and finishing a network call
// for a step already reported as failed.
func inOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills that group. The fallback is the single process, for the case
// where the group was never created because the command failed to start.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
