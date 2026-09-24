package transcript

import (
	"time"

	"golang.org/x/sys/unix"
)

// processStart is when the kernel started pid.
func processStart(pid int) (time.Time, bool) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k.Proc.P_pid != int32(pid) {
		return time.Time{}, false
	}
	st := k.Proc.P_starttime
	return time.Unix(int64(st.Sec), int64(st.Usec)*1000), true
}
