package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Odbiór (acceptance) claim: the lock that stops two acceptance sessions from
// taking the same executor run.
//
// WHERE IT LIVES. Next to the run's own run-state, in
// <projectDir>/.executor/<trackerID>.finish.claim - never next to whoever is
// doing the acceptance. A run and its acceptance can stand on DIFFERENT
// machines (a batch runs on the VPS, the human accepts it on the mac), so a
// lock kept locally by the accepting side would have every machine looking in
// its own directory and both taking the same run - exactly what this prevents.
//
// WHY A TTL AND NOT A PID. AcquireWorktreeLock and LiveSessionHolder decide
// liveness with ProcessAliveSinceStamp(pid, started). That CANNOT be copied
// here: the pid of an acceptance process on the mac, written into a file on the
// VPS, is unverifiable on the VPS - asking about it there answers about a
// completely different process that happens to carry the same number. Instead
// the claim carries a `refreshed` stamp that a running acceptance moves
// forward, and it expires FinishClaimTTL after that stamp. ONE rule for local
// and remote alike, with no "local checks the pid, remote waits out the clock"
// branch: two paths are two paths to get wrong, and the local case loses
// nothing by waiting out a clock.
//
// PID is still recorded, but PURELY informationally (so a message can say
// "held by pid 4711 on host mac since 22 min ago"). It never decides validity.

var (
	// FinishClaimTTL is how long a claim stays valid without a refresh. Past
	// it, the claim is void and anyone may take it over.
	FinishClaimTTL = 10 * time.Minute
	// FinishClaimRefreshInterval is how often a running acceptance moves the
	// `refreshed` stamp forward - a tenfold margin against a sleeping laptop or
	// a slow ssh hop.
	//
	// Both are vars rather than consts so tests can shrink them, exactly like
	// cmd.workerHeartbeatInterval is a var seeded from its const.
	FinishClaimRefreshInterval = 60 * time.Second
)

// finishClaimSeq makes every scratch file this process writes unique. The pid
// alone is not enough: two goroutines in ONE process claiming the same tracker
// would share a scratch name, and the second one's os.WriteFile truncates a
// file the first has already hard-linked into place - corrupting the winner's
// claim through the link.
var finishClaimSeq atomic.Uint64

// FinishClaim is the JSON payload of an acceptance claim: who is accepting this
// run right now.
type FinishClaim struct {
	TrackerID string `json:"tracker_id"`
	Host      string `json:"host"`
	PID       int    `json:"pid"`               // informational only - see the note above
	Session   string `json:"session,omitempty"` // CC session or run id, when known
	Started   string `json:"started"`           // RFC3339
	Refreshed string `json:"refreshed"`         // RFC3339, moved forward while the acceptance runs
}

// FinishClaimBusyError is returned by AcquireFinishClaim when a live (unexpired)
// claim is already held.
type FinishClaimBusyError struct {
	Holder *FinishClaim
}

func (e *FinishClaimBusyError) Error() string {
	return fmt.Sprintf("finish claim for %s is held by %s (pid %d), started %s, refreshed %s ago - "+
		"wait for it to finish or for the claim to expire (%s without a refresh)",
		e.Holder.TrackerID, e.Holder.Host, e.Holder.PID,
		e.Holder.Started, FinishClaimAge(e.Holder.Refreshed), FinishClaimTTL)
}

// FinishClaimPath is the claim path for a tracker, alongside its run-state.
func FinishClaimPath(projectDir, trackerID string) string {
	return filepath.Join(executorRunDir(projectDir), trackerID+".finish.claim")
}

// ReadFinishClaim returns the claim currently written for trackerID, or
// (nil, nil) when there is none. A claim that exists but cannot be parsed
// returns an error - callers deciding liveness treat that as free (see
// LiveFinishClaimHolder), the same way a corrupt session lock does.
func ReadFinishClaim(projectDir, trackerID string) (*FinishClaim, error) {
	data, err := os.ReadFile(FinishClaimPath(projectDir, trackerID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c FinishClaim
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// LiveFinishClaimHolder returns the holder only while the claim is still valid,
// else nil. Expired, corrupt and unreadable all read as FREE: a truncated file
// left by a crashed process must not wall off a run forever - the same rule
// LiveSessionHolder follows.
func LiveFinishClaimHolder(projectDir, trackerID string) *FinishClaim {
	c, err := ReadFinishClaim(projectDir, trackerID)
	if err != nil || c == nil {
		return nil
	}
	if c.Expired(time.Now()) {
		return nil
	}
	return c
}

// Expired reports whether the claim has gone stale by now - i.e. it was not
// refreshed within FinishClaimTTL. A missing or unparseable `refreshed` stamp
// counts as expired: the claim carries no evidence that anyone is still on it,
// and the alternative (honouring it forever) is the wall-off this design
// exists to avoid.
func (c *FinishClaim) Expired(now time.Time) bool {
	ts, err := time.Parse(time.RFC3339, c.Refreshed)
	if err != nil {
		return true
	}
	return now.Sub(ts) > FinishClaimTTL
}

// AcquireFinishClaim claims trackerID for this host/process. A live claim held
// by anyone (including this same process - one run gets one acceptance) returns
// *FinishClaimBusyError with the holder. An EXPIRED or corrupt claim is taken
// over automatically.
//
// The claim is ATOMIC, with the two primitives AcquireWorktreeLock established:
//   - claim = link(2) of a fully written scratch file onto the claim path, so
//     a rival never observes a half-written winner (a plain O_EXCL create +
//     write has exactly that window). On EEXIST we read the incumbent and
//     report it busy.
//   - takeover of an expired claim = rename it aside, then retry in a loop. Two
//     simultaneous takeovers race on the rename; the loser's rename fails with
//     ENOENT and its next attempt sees the winner's fresh claim.
func AcquireFinishClaim(projectDir, trackerID, session string) (*FinishClaim, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	claim := &FinishClaim{
		TrackerID: trackerID,
		Host:      hostname(),
		PID:       os.Getpid(),
		Session:   session,
		Started:   now,
		Refreshed: now,
	}
	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return nil, err
	}
	dir := executorRunDir(projectDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	path := FinishClaimPath(projectDir, trackerID)

	for attempt := 0; attempt < 5; attempt++ {
		tmp := scratchPath(path, "tmp")
		if err := os.WriteFile(tmp, data, 0644); err != nil {
			return nil, err
		}
		linkErr := os.Link(tmp, path)
		_ = os.Remove(tmp)
		if linkErr == nil {
			return claim, nil
		}
		if !os.IsExist(linkErr) {
			return nil, linkErr
		}

		existing, rerr := ReadFinishClaim(projectDir, trackerID)
		if rerr == nil {
			if existing == nil {
				continue // released or stolen between EEXIST and the read - retry
			}
			if !existing.Expired(time.Now()) {
				return nil, &FinishClaimBusyError{Holder: existing}
			}
		}
		// Expired, or corrupt (rerr != nil - a truncated file from a crashed
		// process must not brick the run). Steal it atomically: rename aside and
		// loop to re-claim. If a rival steals first our rename fails harmlessly
		// and the next attempt sees their fresh claim.
		steal := scratchPath(path, "steal")
		if os.Rename(path, steal) == nil {
			_ = os.Remove(steal)
		}
	}
	return nil, fmt.Errorf("could not acquire finish claim at %s (takeover contention)", path)
}

// RefreshFinishClaim moves own's `refreshed` stamp forward, keeping the claim
// alive past the TTL, and updates own in place so the caller's copy stays
// current. If the claim was taken over in the meantime (a different
// Host+Started) it returns an error INSTEAD of overwriting somebody else's.
//
// The write goes through a scratch file + rename, never in place: other
// machines and processes read this file concurrently, would see the truncated
// middle of an in-place write as corrupt, and would then steal it from a live
// holder - the same reason AcquireWorktreeLock renames its re-entrant refresh.
func RefreshFinishClaim(projectDir, trackerID string, own *FinishClaim) error {
	if own == nil {
		return fmt.Errorf("refresh finish claim for %s: no claim to refresh", trackerID)
	}
	current, err := ReadFinishClaim(projectDir, trackerID)
	if err != nil {
		return fmt.Errorf("refresh finish claim for %s: %w", trackerID, err)
	}
	if current == nil {
		return fmt.Errorf("refresh finish claim for %s: the claim is gone (expired and taken over, or released)", trackerID)
	}
	if !sameFinishClaim(current, own) {
		return fmt.Errorf("refresh finish claim for %s: it is now held by %s (pid %d) since %s, not by us",
			trackerID, current.Host, current.PID, current.Started)
	}

	refreshed := *own
	refreshed.Refreshed = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(&refreshed, "", "  ")
	if err != nil {
		return err
	}
	path := FinishClaimPath(projectDir, trackerID)
	tmp := scratchPath(path, "refresh")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	own.Refreshed = refreshed.Refreshed
	return nil
}

// ReleaseFinishClaim removes the claim, but ONLY when it is still own's (same
// Host+Started). A claim that has moved on to somebody else is left untouched
// and reported as an error - releasing it would hand their run to a third
// session. A claim that is already gone is a no-op (release is idempotent).
func ReleaseFinishClaim(projectDir, trackerID string, own *FinishClaim) error {
	if own == nil {
		return fmt.Errorf("release finish claim for %s: no claim to release", trackerID)
	}
	current, err := ReadFinishClaim(projectDir, trackerID)
	if err != nil {
		return fmt.Errorf("release finish claim for %s: %w", trackerID, err)
	}
	if current == nil {
		return nil
	}
	if !sameFinishClaim(current, own) {
		return fmt.Errorf("release finish claim for %s: it is now held by %s (pid %d) since %s, not by us",
			trackerID, current.Host, current.PID, current.Started)
	}
	err = os.Remove(FinishClaimPath(projectDir, trackerID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// sameFinishClaim is the ownership test: Host+Started. The pid is deliberately
// NOT part of it - it is unverifiable across machines, and the process that
// wrote the claim (e.g. `pm finish claim`) may have exited long before the
// acceptance it stands for is over.
func sameFinishClaim(a, b *FinishClaim) bool {
	return a.Host == b.Host && a.Started == b.Started
}

// scratchPath builds a per-process, per-call scratch name next to the claim, so
// concurrent writers never share one (see finishClaimSeq).
func scratchPath(path, kind string) string {
	return fmt.Sprintf("%s.%s.%d.%d", path, kind, os.Getpid(), finishClaimSeq.Add(1))
}

// Hostname is the host identity written into a claim (and the one a CLI
// release compares against). Exported so a caller can ask "is this claim
// ours?" without re-deriving the fallback.
func Hostname() string { return hostname() }

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown-host"
	}
	return h
}

// FinishClaimAge renders how long ago an RFC3339 stamp was, for humans - the
// one formatting of a claim's age, shared by the busy error and `pm finish
// status` so the two never drift.
func FinishClaimAge(stamp string) string {
	ts, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return "unknown"
	}
	d := time.Since(ts)
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}
