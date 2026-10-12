package storage

import (
	"fmt"
	"os"
	"sync/atomic"
)

// scratchSeq makes every scratch file this process writes unique per call.
// The pid alone is not enough: two goroutines in ONE process (the epic
// manager refreshing its own lock while a release runs, two claimers of one
// tracker) would share a scratch name, and the second os.WriteFile would
// truncate a file the first has already hard-linked into place, corrupting
// the winner's lock or claim through the link.
var scratchSeq atomic.Uint64

// scratchPath builds a per-process, per-call scratch name next to path, so
// concurrent writers never share one and a rename to path stays on one
// filesystem. It starts with path's own name on purpose: a pattern that hides
// the target hides its scratch files too (`.pm-executor.lock*` in a
// worktree's git exclude). Keyed on THIS process's own pid (os.Getpid()),
// never on a lock's PID field: a release can legitimately act on behalf of a
// foreign pid (killRun cleans up a dead worker's lock from the board process,
// see ReleaseWorktreeLock), and keying on that target pid would let two
// independent releasing PROCESSES - each with its own scratchSeq starting at
// 0 - compute the identical scratch name for the same dead holder.
func scratchPath(path, kind string) string {
	return fmt.Sprintf("%s.%s.%d.%d", path, kind, os.Getpid(), scratchSeq.Add(1))
}

// atomicWriteFile replaces path with data: it writes a scratch file next to
// path and renames it over the target, so a reader (another pm process, the
// board's slot indicator, a rival's busy check) sees the old file or the new
// one whole, never a torn mixture, and a crash leaves the old file intact.
// The scratch is removed on every failure, the rename's included. perm is set
// exactly (Chmod), not filtered through the umask.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := scratchPath(path, "tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
