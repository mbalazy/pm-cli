//go:build darwin

package storage

import (
	"time"

	"golang.org/x/sys/unix"
)

// processStartTime reads the process start time from the kernel
// (KERN_PROC_PID), not from `ps`: the sysctl is a syscall rather than a fork,
// and this runs on the board's render path. The second return is false when the
// answer is unavailable (no such process, a kernel that answered with a
// different pid, a zero stamp) - the caller then falls back to bare liveness.
func processStartTime(pid int) (time.Time, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kp == nil {
		return time.Time{}, false
	}
	if kp.Proc.P_pid != int32(pid) {
		return time.Time{}, false
	}
	tv := kp.Proc.P_starttime
	if tv.Sec == 0 && tv.Usec == 0 {
		return time.Time{}, false
	}
	return time.Unix(tv.Sec, int64(tv.Usec)*1000), true
}
