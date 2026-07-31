package storage

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// LockProject takes an EXCLUSIVE cross-process advisory lock on the project's
// data dir (flock on <dir>/.pm.lock), serializing read-modify-write cycles on
// task files across pm processes - MCP servers of parallel CC sessions,
// executor managers, the board. Without it two writers interleave
// (read A, read B, write A, write B) and B silently drops A's update; task
// writes themselves are atomic (tmp+rename), so the lost update is the ONLY
// concurrency hazard. Callers hold the lock around FindTask -> mutate ->
// WriteTask, reading the task FRESH inside the critical section.
//
// Blocking (writers queue), advisory (a direct file edit in an editor bypasses
// it - acceptable for a human-paced action), unix-only (flock).
//
// Never nest LockProject calls in one process: the second flock is on a fresh
// fd and would deadlock against our own lock. Keep acquisitions at the leaf
// (handler/apply) level.
func (s *Store) LockProject(slug string) (func(), error) {
	dir := s.ProjectDir(slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".pm.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	var once sync.Once
	// Idempotent: callers may release early (to hand off to a self-locking
	// callee like MoveTask) AND still have the deferred release fire.
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		})
	}, nil
}

// lockProjectIfExists takes the project lock only when the project dir is
// already there, and never fails: a lock that cannot be taken degrades to an
// unlocked write (today's behaviour) rather than dropping the caller's edit.
//
// The existence check is load-bearing, not defensive. LockProject MkdirAll's
// the dir (the lock file lives inside it), so locking a slug with no dir would
// CREATE it - turning a write to a typo'd slug from an error into a silently
// created project, and re-creating the dir of a project just deleted. With no
// dir there is nothing to serialize against anyway: the write that follows
// fails on its own, exactly as it did before the lock existed.
func (s *Store) lockProjectIfExists(slug string) func() {
	noop := func() {}
	if _, err := os.Stat(s.ProjectDir(slug)); err != nil {
		return noop
	}
	release, err := s.LockProject(slug)
	if err != nil {
		return noop
	}
	return release
}
