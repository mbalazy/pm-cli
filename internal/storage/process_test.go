package storage

import (
	"os"
	"testing"
	"time"
)

// deadPID is high enough to be unused on both platforms pm runs on (Linux caps
// pid_max well below it by default, macOS lower still).
const deadPID = 2147483646

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Error("our own process must read as alive")
	}
	if ProcessAlive(deadPID) {
		t.Errorf("pid %d must read as dead", deadPID)
	}
	if ProcessAlive(0) || ProcessAlive(-1) {
		t.Error("a non-positive pid is never alive")
	}
	// pid 1 belongs to root, so signal 0 comes back EPERM for an ordinary user.
	// A process we may not signal is one that EXISTS - reading it as dead is the
	// direction that steals a live holder's lock.
	if !ProcessAlive(1) {
		t.Error("pid 1 must read as alive (EPERM means present, not gone)")
	}
}

func TestProcessStartTime(t *testing.T) {
	start, ok := processStartTime(os.Getpid())
	if !ok {
		t.Fatal("darwin and linux must both report a start time - without it the recycled-pid check silently degrades")
	}
	if start.After(time.Now()) {
		t.Errorf("start time %s is in the future", start)
	}
	if time.Since(start) > time.Hour {
		t.Errorf("start time %s is implausible for a test binary", start)
	}
	if _, ok := processStartTime(deadPID); ok {
		t.Error("a dead pid must not report a start time")
	}
}

func TestProcessAliveSinceRejectsRecycledPID(t *testing.T) {
	self := os.Getpid()

	// The reference is when the holder stamped its lock. Our own process was
	// running by then, so it is still the holder.
	if !ProcessAliveSince(self, time.Now()) {
		t.Error("a process that started before the stamp is the holder")
	}

	// The recycled shape: a lock stamped a day ago, held by a pid whose current
	// owner started minutes ago. That is somebody else wearing the number.
	if ProcessAliveSince(self, time.Now().Add(-24*time.Hour)) {
		t.Error("a process that started AFTER the stamp cannot be the holder")
	}

	// A dead pid stays dead whatever the stamp says.
	if ProcessAliveSince(deadPID, time.Now()) {
		t.Error("a dead pid is never alive")
	}

	// Missing information degrades to bare liveness - never to "dead", which is
	// the answer that steals a lock.
	if !ProcessAliveSince(self, time.Time{}) {
		t.Error("a zero reference must degrade to bare liveness")
	}
}

func TestProcessAliveSinceStamp(t *testing.T) {
	self := os.Getpid()

	if !ProcessAliveSinceStamp(self, time.Now().Format(time.RFC3339)) {
		t.Error("a fresh stamp must read as held")
	}
	if ProcessAliveSinceStamp(self, "2020-01-01T00:00:00Z") {
		t.Error("a stamp from before this process existed must read as recycled")
	}
	for _, stamp := range []string{"", "not a timestamp"} {
		if !ProcessAliveSinceStamp(self, stamp) {
			t.Errorf("stamp %q: an unreadable stamp must degrade to bare liveness", stamp)
		}
	}
}

// The grace margin exists because the stamp is RFC3339 (whole seconds) and is
// written a moment AFTER the holder starts: without it a lock stamped in the
// same second the holder booted could read as recycled and be stolen from a
// live run.
func TestProcessStartGraceAbsorbsStampTruncation(t *testing.T) {
	start, ok := processStartTime(os.Getpid())
	if !ok {
		t.Skip("no start time on this platform")
	}
	stamp := start.Add(-time.Second).Truncate(time.Second)
	if !ProcessAliveSince(os.Getpid(), stamp) {
		t.Errorf("a stamp %s truncated just before our start %s must not read as recycled", stamp, start)
	}
}
