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
	// The lock file lives INSIDE the project dir, so creating the dir here
	// (as this used to) made every lock site able to conjure a project: a
	// write to a typo'd slug silently created one, and locking a project
	// someone just deleted resurrected its dir - `applyWorkerResult` then
	// wrote a 30-minute-old task into it, invisible forever since no
	// project.yaml comes back with it. Locking something that does not exist
	// is a caller error; CreateProject (the only site that legitimately makes
	// the dir) MkdirAll's before it locks. Callers that degrade to an
	// unlocked write on error keep exactly the behaviour they had.
	if _, err := os.Stat(dir); err != nil {
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
