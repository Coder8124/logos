//go:build !windows

package setup

import (
	"os/exec"
	"syscall"
	"time"
)

// inOwnProcessGroup starts claude in a process group of its own so a timed-out
// step can take down everything it started. `claude` is a shell wrapper around
// node: killing the process logos launched left the node process running,
// holding the pipes this package is still reading and finishing a network call
// for a step already reported as failed.
func inOwnProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree kills that group, and keeps killing it until it is empty. One
// SIGKILL is not enough: a child forked in the instant the signal was
// delivered joins the group afterwards and survives, still holding the pipes
// and still finishing the step's work. Signalling again reaches it. The loop
// is bounded so a process that cannot be killed does not hang the step.
// The fallback is the single process, for the case where the group was never
// created because the command failed to start.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid := cmd.Process.Pid
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == syscall.ESRCH {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}
