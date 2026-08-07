//go:build linux

package storage

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is the kernel's USER_HZ, the unit of field 22 in /proc/<pid>/stat.
// It is 100 on every Linux architecture pm runs on (the VPS runner included);
// reading it properly needs sysconf(_SC_CLK_TCK), i.e. cgo, for a constant that
// has not moved in decades.
const clockTicks = 100

// processStartTime reads the process start time from /proc: field 22 of
// /proc/<pid>/stat is the start time in clock ticks since boot, and btime in
// /proc/stat is when boot was. Cheap file reads, no fork - this runs on the
// board's render path. The second return is false when the answer is
// unavailable, and the caller then falls back to bare liveness.
func processStartTime(pid int) (time.Time, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, false
	}
	// Field 2 is the executable name in parentheses and may itself contain
	// spaces and parentheses, so the fields are counted from the LAST ')'.
	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return time.Time{}, false
	}
	fields := strings.Fields(string(data)[end+1:])
	// Field 22 overall = index 19 after the state field, which is the first one
	// following the ')'.
	const startTimeIdx = 19
	if len(fields) <= startTimeIdx {
		return time.Time{}, false
	}
	ticks, err := strconv.ParseInt(fields[startTimeIdx], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	boot, ok := bootTime()
	if !ok {
		return time.Time{}, false
	}
	return boot.Add(time.Duration(ticks) * time.Second / clockTicks), true
}

// bootTime reads btime (seconds since the epoch) out of /proc/stat.
func bootTime() (time.Time, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, found := strings.CutPrefix(line, "btime ")
		if !found {
			continue
		}
		secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(secs, 0), true
	}
	return time.Time{}, false
}
