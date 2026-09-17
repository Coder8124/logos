//go:build windows

package setup

import "os/exec"

// Windows has no process groups to inherit in the POSIX sense, and killing a
// job object is more machinery than a bounded plugin install is worth here, so
// the timeout takes out the process it started and no more.
func inOwnProcessGroup(cmd *exec.Cmd) {}

func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
