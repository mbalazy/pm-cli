package storage

import (
	"os"
	"path/filepath"
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
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
