//go:build unix

package transcript

import (
	"errors"
	"syscall"
)

// processAlive is signal 0: delivered to nobody, refused only when there is no
// such process. EPERM means one exists that belongs to someone else.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
