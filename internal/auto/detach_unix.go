//go:build !windows

package auto

import (
	"os/exec"
	"syscall"
)

// detach puts the dream in a session of its own, so the terminal or host that
// started it closing does not take it down halfway through a write.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
