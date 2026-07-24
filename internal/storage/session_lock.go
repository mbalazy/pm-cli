package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Interactive-session slot registry.
//
// An interactive worktree launch (board `c` -> Worktree here/tmux) gets a
// worktree slot's merged env (Metro port, sim UDID, ...) injected. To let two
// interactive sessions coexist on different slots, each launch claims a
// per-slot session lock. pm cannot write the lock itself (the claude process
// does not exist yet at pick time), so the launch runs through `sh -c 'write
// lock with $$; exec claude ...'` - after exec the shell's pid IS claude's
// pid, giving the lock a live, verifiable holder just like the executor's
// worktree lock. A lock whose pid is dead is stale and reads as free; nothing
// ever needs to clean it up explicitly.
//
// The locks live under the pm project DATA dir (<pm-root>/<slug>/.sessions/),
// never inside the slot's worktree directory: creating that directory as a
// side effect would poison EnsureWorktree, which refuses a pre-existing
// non-worktree path. The executor's .pm-executor.lock is a SEPARATE resource
// guarding the worktree directory; session locks guard the slot's runtime env
// (port/sim), which headless workers never use (PM_HEADLESS).

// SessionLock is the JSON payload of an interactive-session slot lock.
type SessionLock struct {
	PID       int    `json:"pid"`
	SessionID string `json:"session_id"`
	Slot      int    `json:"slot"`    // 1-based slot index
	Started   string `json:"started"` // RFC3339
}

// SessionLockPath returns the lock path for a 1-based slot index under the pm
// project data dir.
func SessionLockPath(projectDir string, slot int) string {
	return filepath.Join(projectDir, ".sessions", fmt.Sprintf("slot-%d.lock", slot))
}

// ReadSessionLock returns the session lock for a slot, or (nil, nil) when the
// slot has never been claimed.
func ReadSessionLock(projectDir string, slot int) (*SessionLock, error) {
	data, err := os.ReadFile(SessionLockPath(projectDir, slot))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lk SessionLock
	if err := json.Unmarshal(data, &lk); err != nil {
		return nil, err
	}
	return &lk, nil
}

// LiveSessionHolder returns the slot's lock when it is held by a LIVE
// process, else nil (free, stale, or unreadable/corrupt - a broken lock must
// not brick the slot).
func LiveSessionHolder(projectDir string, slot int) *SessionLock {
	lk, err := ReadSessionLock(projectDir, slot)
	if err != nil || lk == nil {
		return nil
	}
	if !ProcessAlive(lk.PID) {
		return nil
	}
	return lk
}
