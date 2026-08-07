package storage

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Process liveness is TWO questions, and pm used to ask only the cheap one.
//
// "Does a process with this pid exist" is what signal 0 answers. What every
// caller actually means is "is the process that wrote this lock still running",
// and pids are RECYCLED - after a reboot or a heavy fork churn the number comes
// back around and lands on somebody else. The consequences were not theoretical:
// a `kill -9`'d or reboot-orphaned `.pm-executor.lock` reads as held by whatever
// process later inherits the number, so the worktree slot stays busy until a
// human deletes the file, and `pm executor stats` books a crashed run as still
// running. The journal already learned this lesson on the other side of the same
// hazard ("pids get recycled; run ids do not" - the run_id fix, 0.27.0); the
// locks never got the same treatment.
//
// The second criterion is the process START TIME against the stamp the holder
// wrote. A pid cannot be reused while its owner is alive, so a process that
// started BEFORE the lock was stamped is necessarily the one that stamped it,
// and one that started AFTER it is necessarily not.

// processStartGrace absorbs the distance between a holder starting and stamping
// its lock: the stamp is RFC3339 (second granularity, so up to a second is
// truncated away) and a little startup skew rides on top. Only a start time
// LATER than the stamp by more than this counts as recycling - the margin costs
// nothing, since recycling a pid within seconds of the stamp is not the failure
// this exists to catch (reboots and long-dead locks are).
const processStartGrace = 5 * time.Second

// ProcessAlive reports whether pid refers to a live process (Unix: signal 0).
//
// EPERM counts as ALIVE: a process owned by another user is one we may not
// signal, not one that is gone. The error directions are not symmetric - reading
// a live holder as dead steals its lock and corrupts a running job, reading a
// dead one as alive only costs a wait - so the ambiguous answer resolves towards
// alive.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ProcessAliveSince reports whether pid is alive AND could still be the process
// that was running at ref - i.e. it did not start after it.
//
// It degrades to bare ProcessAlive whenever the second criterion cannot be
// applied: a zero ref (no stamp to compare against) or a platform that cannot
// report a start time. Missing information must never turn into "dead", which
// is the answer that steals a lock.
func ProcessAliveSince(pid int, ref time.Time) bool {
	if !ProcessAlive(pid) {
		return false
	}
	if ref.IsZero() {
		return true
	}
	start, ok := processStartTime(pid)
	if !ok {
		return true
	}
	return !start.After(ref.Add(processStartGrace))
}

// ProcessAliveSinceStamp is ProcessAliveSince over an RFC3339 stamp, the form
// the lock files and run-states store. An unparseable or empty stamp degrades to
// bare liveness rather than to "dead".
func ProcessAliveSinceStamp(pid int, stamp string) bool {
	ts, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return ProcessAlive(pid)
	}
	return ProcessAliveSince(pid, ts)
}
