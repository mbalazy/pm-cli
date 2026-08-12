package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Acceptance claim: the lock that stops two acceptance sessions from
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
//
// WHAT "BOTH MACHINES SEE IT" MEANS. The claim is a file in the pm data dir of
// the machine the RUN stands on, so a second machine reaches it the way it
// reaches that pm at all - over ssh, running pm there (`ssh runner pm finish
// claim ...`). Nothing here mounts or syncs anything: two acceptances collide
// safely only while both operate the SAME data dir, which is exactly why the
// claim sits with the run rather than with the accepting side.
//
// WHAT THE TTL ASSUMES. Comparing a stamp written on one machine against
// another machine's clock assumes the two are roughly in step (ntp-level, not
// exact). A machine whose clock lags by more than the TTL writes claims that
// read as expired everywhere else; one that leads writes claims that outlive
// their holder by the skew. That is the price of dropping the pid check, which
// carried no such assumption - and the reason the TTL is minutes rather than
// seconds.

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

// claimTimeLayout stamps a claim with NANOSECOND precision. The stamps are
// still RFC3339 (time.Parse with the plain RFC3339 layout reads them, so a
// hand-written second-granularity claim keeps working), but `started` is half
// of the claim's IDENTITY - see sameFinishClaim - and second granularity makes
// two claims taken on one host inside the same second indistinguishable, so a
// stale copy of a released claim would pass the ownership test against its
// successor and refresh or release somebody else's run.
const claimTimeLayout = time.RFC3339Nano

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
	Started   string `json:"started"`           // RFC3339, also half of the claim's identity
	Refreshed string `json:"refreshed"`         // RFC3339, moved forward while the acceptance runs
}

// FinishClaimBusyError is returned by AcquireFinishClaim when a live (unexpired)
// claim is already held.
type FinishClaimBusyError struct {
	Holder *FinishClaim
}

func (e *FinishClaimBusyError) Error() string {
	if e.Holder == nil {
		return "finish claim is held by another acceptance session"
	}
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
	return readClaimFile(FinishClaimPath(projectDir, trackerID))
}

// readClaimFile reads a claim from an exact path (the live claim, or one
// renamed aside mid-takeover). A parse failure is wrapped in
// *corruptClaimError so a caller can tell "this file's CONTENT is garbage"
// from "I could not read the file": the first justifies stealing the claim,
// the second (EACCES, EIO, a stale handle on a shared dir) says nothing about
// the holder and must never cost them their run.
func readClaimFile(path string) (*FinishClaim, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c FinishClaim
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, &corruptClaimError{Path: path, Err: err}
	}
	return &c, nil
}

// corruptClaimError marks a claim file whose CONTENT could not be parsed.
type corruptClaimError struct {
	Path string
	Err  error
}

func (e *corruptClaimError) Error() string {
	return fmt.Sprintf("unreadable finish claim %s: %v", e.Path, e.Err)
}

func (e *corruptClaimError) Unwrap() error { return e.Err }

// IsCorruptFinishClaim reports whether err came from an unparseable claim FILE,
// as opposed to an I/O failure while reading it. The distinction decides who
// may DESTROY the claim: garbage content is nobody's run, an unreadable file
// may well be somebody's.
func IsCorruptFinishClaim(err error) bool {
	var c *corruptClaimError
	return errors.As(err, &c)
}

// LiveFinishClaimHolder returns the holder only while the claim is still valid,
// else nil. Expired, corrupt and unreadable all read as FREE: a truncated file
// left by a crashed process must not wall off a run forever - the same rule
// LiveSessionHolder follows. (Reading unreadable as free is safe HERE because
// this answers a question; AcquireFinishClaim, which DESTROYS what it reads as
// free, is deliberately stricter - see isCorruptClaim.)
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

// LiveFinishClaims returns every live acceptance claim in the project dir,
// keyed by tracker id. The board's tick reads this to show a HAND-DRIVEN
// acceptance - a claim taken via `pm finish claim` (e.g. a batch-finish-auto
// session), which never writes a .finish.json - on the tracker's card and in
// the detail view, the same "a live claim wins" rule acceptCell follows in the
// Runs view.
//
// A file vouches for its own tracker the way readRunStatesDir's states do: the
// claim counts only when the path its TrackerID would be written to is the
// very file it was read from. Matching on the name alone would not survive a
// dot in a task id (ValidateTaskID permits one): `x.finish.claim` is the claim
// of tracker `x`, while a tracker literally named `x.finish` claims at
// `x.finish.finish.claim`. Expired, corrupt and unreadable entries are skipped
// - each already reads as free everywhere else (see LiveFinishClaimHolder).
func LiveFinishClaims(projectDir string) map[string]*FinishClaim {
	out := map[string]*FinishClaim{}
	entries, err := os.ReadDir(executorRunDir(projectDir))
	if err != nil {
		return out
	}
	now := time.Now()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".finish.claim") {
			continue
		}
		c, err := readClaimFile(filepath.Join(executorRunDir(projectDir), name))
		if err != nil || c == nil {
			continue
		}
		if filepath.Base(FinishClaimPath(projectDir, c.TrackerID)) != name {
			continue
		}
		if c.Expired(now) {
			continue
		}
		out[c.TrackerID] = c
	}
	return out
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
//   - takeover of an expired claim = rename it aside, CHECK WHAT CAME ASIDE,
//     then retry in a loop. The check is not optional: the decision to steal is
//     made before the rename, so a rival that won the takeover in between can
//     have linked its own FRESH claim at the path by the time we rename - and
//     deleting that would hand the same run to two acceptances, each holding a
//     nil error. What we move aside is therefore inspected and, when it turns
//     out to be someone's live claim, put straight back; the next attempt then
//     reports it busy.
func AcquireFinishClaim(projectDir, trackerID, session string) (*FinishClaim, error) {
	// The tracker id becomes a file name under .executor/ - validate it where
	// it is WRITTEN, like every other identifier in this package (see
	// ValidateTaskID / ValidateSlug). Unvalidated, `../../x` would place (and,
	// via release, unlink) a file outside the project dir.
	if err := ValidateTaskID(trackerID); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(claimTimeLayout)
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
			_ = os.Remove(tmp) // names are unique per call, so a leftover never gets reused
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
		switch {
		case rerr != nil && !IsCorruptFinishClaim(rerr):
			// The file is there and we could not READ it (EACCES, EIO, a stale
			// handle). That says nothing about the holder, and the branch below
			// DESTROYS what it steals - so refuse rather than delete a live
			// claim on the strength of an unrelated failure.
			return nil, fmt.Errorf("read finish claim at %s: %w", path, rerr)
		case rerr == nil && existing == nil:
			continue // released or stolen between EEXIST and the read - retry
		case rerr == nil && !existing.Expired(time.Now()):
			return nil, &FinishClaimBusyError{Holder: existing}
		}
		// Expired, or corrupt (a truncated file from a crashed process must not
		// brick the run). Steal it: rename aside, verify, loop to re-claim.
		if err := stealExpiredClaim(path); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not acquire finish claim at %s (takeover contention)", path)
}

// stealExpiredClaim moves the claim at path aside so the caller can re-claim,
// and DELETES what it moved only once it has proved that file is void (expired
// or unparseable). Everything else is put back, or - when it cannot be put back
// - left on disk with an error, because the alternative is deleting a claim
// somebody is holding.
//
// The verification is the whole point: the decision to steal is taken before
// the rename, so by then the path may hold a rival's fresh claim (they won the
// same takeover) or a file that has become unreadable. Round one of this code
// deleted both, which grants one run to two acceptances.
//
// Restoring goes through link+remove, never rename: rename would silently
// overwrite a THIRD claim that landed in the meantime, which is the same
// double-grant one step further out. If the restoring link fails, the file
// stays under its scratch name and the error names it - litter a human can put
// back beats a claim nobody can get back.
//
// What this does NOT close: between the rename and the restore the path is
// empty, so a third actor can claim the run in that window and the restore then
// fails. The window is short and its outcome is now an error rather than a
// silent double grant, but it is real.
func stealExpiredClaim(path string) error {
	steal := scratchPath(path, "steal")
	if os.Rename(path, steal) != nil {
		// A rival stole it first (ENOENT) - harmless, the next attempt sees
		// their fresh claim.
		return nil
	}
	stolen, err := readClaimFile(steal)
	switch {
	case err != nil && !IsCorruptFinishClaim(err):
		// Could not READ what we moved aside - it may well be a live claim (see
		// the same distinction in AcquireFinishClaim). Put it back if we can,
		// and refuse either way rather than delete it.
		if os.Link(steal, path) == nil {
			_ = os.Remove(steal)
			return fmt.Errorf("read finish claim at %s: %w", path, err)
		}
		return fmt.Errorf("read finish claim at %s: %w (it is now at %s - move it back)", path, err, steal)
	case err == nil && stolen != nil && !stolen.Expired(time.Now()):
		// A rival won this takeover and linked a fresh claim. Restore it; the
		// next attempt reads it and reports busy.
		if os.Link(steal, path) == nil {
			_ = os.Remove(steal)
			return nil
		}
		return fmt.Errorf("another acceptance claimed %s while we were taking it over, and its claim could not be "+
			"restored - it is at %s, move it back", path, steal)
	}
	// Proved void: expired, unparseable, or gone.
	_ = os.Remove(steal)
	return nil
}

// RefreshFinishClaim moves the claim's `refreshed` stamp forward - keeping it
// alive past the point it would otherwise have expired - and updates own in
// place so the caller's copy stays current. It refuses, rather than writing, in
// three cases: the claim is gone, it is now somebody else's (a different
// Host+Started), or it is expired OR WITHIN ONE REFRESH TICK of expiring.
//
// The expiry case is the subtle one. Every other surface already reports an
// expired claim as free - LiveFinishClaimHolder returns nil, `pm finish status`
// prints "free", AcquireFinishClaim steals it - so refreshing one would revive
// a claim a rival may already have taken over, leaving two acceptances each
// convinced they hold the run. The refusal starts one FinishClaimRefreshInterval
// EARLY because this is check-then-write: a claim that passes the check and then
// lapses while the process is descheduled (a loaded VPS, a wake from sleep, the
// ssh hop) would have its rename land on the rival's fresh claim. One tick of
// margin is the same margin the refresher already has - it ticks ten times per
// TTL - so nothing legitimate reaches it. A holder that lapsed anyway has to
// re-Acquire, which is the atomic path and reports the rival honestly.
//
// The write goes through a scratch file + rename, never in place: other
// machines and processes read this file concurrently, would see the truncated
// middle of an in-place write as corrupt, and would then steal it from a live
// holder - the same reason AcquireWorktreeLock renames its re-entrant refresh.
func RefreshFinishClaim(projectDir, trackerID string, own *FinishClaim) error {
	if own == nil {
		return fmt.Errorf("refresh finish claim for %s: no claim to refresh", trackerID)
	}
	if err := ValidateTaskID(trackerID); err != nil {
		return err
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
	if lapsing(current, time.Now()) {
		return fmt.Errorf("refresh finish claim for %s: it was last refreshed %s ago, too close to the %s TTL to be "+
			"refreshed safely (a rival may be taking it over right now) - take it again with a fresh claim instead",
			trackerID, FinishClaimAge(current.Refreshed), FinishClaimTTL)
	}

	// The payload is the claim ON DISK with a new stamp, never the caller's
	// struct: a refresher that rebuilt its claim from what `pm finish claim`
	// printed carries only Host+Started (that is the whole identity), and
	// writing that would wipe the pid, the session and the tracker id the busy
	// message and `pm finish status` report.
	refreshed := *current
	refreshed.Refreshed = time.Now().UTC().Format(claimTimeLayout)
	data, err := json.MarshalIndent(&refreshed, "", "  ")
	if err != nil {
		return err
	}
	path := FinishClaimPath(projectDir, trackerID)
	tmp := scratchPath(path, "refresh")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	own.Refreshed = refreshed.Refreshed
	return nil
}

// lapsing reports whether a claim is expired, or so close to expiring that a
// refresh could not land before a rival started taking it over. See
// RefreshFinishClaim for why the margin exists.
func lapsing(c *FinishClaim, now time.Time) bool {
	ts, err := time.Parse(time.RFC3339, c.Refreshed)
	if err != nil {
		return true
	}
	return now.Sub(ts) > FinishClaimTTL-FinishClaimRefreshInterval
}

// ReleaseFinishClaim removes the claim, but ONLY when it is still own's (same
// Host+Started). A claim that has moved on to somebody else is left untouched
// and reported as an error - releasing it would hand their run to a third
// session. A claim that is already gone is a no-op (release is idempotent).
//
// The removal is NOT a plain unlink of the path. Between reading the claim and
// deleting it, a rival can take over an expired claim and link its own fresh
// one, and unlinking by path would then delete THEIRS - the double grant this
// lock exists to prevent, arrived at from the other end. So the file is moved
// aside first and only deleted once the copy in hand is confirmed to be ours;
// anything else goes back where it came from.
func ReleaseFinishClaim(projectDir, trackerID string, own *FinishClaim) error {
	if own == nil {
		return fmt.Errorf("release finish claim for %s: no claim to release", trackerID)
	}
	// The id names the file this call UNLINKS - validate it here too, not only
	// in AcquireFinishClaim (see ValidateTaskID there).
	if err := ValidateTaskID(trackerID); err != nil {
		return err
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

	path := FinishClaimPath(projectDir, trackerID)
	aside := scratchPath(path, "release")
	if err := os.Rename(path, aside); err != nil {
		if os.IsNotExist(err) {
			return nil // released or taken over between the read and here
		}
		return err
	}
	moved, rerr := readClaimFile(aside)
	if rerr == nil && moved != nil && !sameFinishClaim(moved, own) {
		// It changed hands in the window. Put it back and say so, rather than
		// deleting a claim somebody else is holding.
		if os.Link(aside, path) == nil {
			_ = os.Remove(aside)
			return fmt.Errorf("release finish claim for %s: it changed hands while we were releasing it "+
				"(now %s, pid %d, since %s) - left it alone", trackerID, moved.Host, moved.PID, moved.Started)
		}
		return fmt.Errorf("release finish claim for %s: it changed hands while we were releasing it and could not "+
			"be restored - it is at %s, move it back", trackerID, aside)
	}
	_ = os.Remove(aside)
	return nil
}

// sameFinishClaim is the ownership test: Host+Started. The pid is deliberately
// NOT part of it - it is unverifiable across machines, and the process that
// wrote the claim (e.g. `pm finish claim`) may have exited long before the
// acceptance it stands for is over. Started therefore has to be unique per
// claim on a host, which is why it is stamped with claimTimeLayout's
// nanoseconds rather than whole seconds.
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
