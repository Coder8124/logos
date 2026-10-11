//go:build windows

package auto

import (
	"os/exec"
	"syscall"
)

// detachedProcess is DETACHED_PROCESS, which syscall does not name.
const detachedProcess = 0x00000008

// detach gives the dream no console and a process group of its own, so the
// host that started it closing does not take it down halfway through a write.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
