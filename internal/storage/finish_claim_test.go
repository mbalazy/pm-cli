package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// seedClaim writes a claim file directly, so a test can plant one that looks
// like it came from another host/process (or from long ago).
func seedClaim(t *testing.T, dir string, c FinishClaim) {
	t.Helper()
	if err := os.MkdirAll(executorRunDir(dir), 0755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal claim: %v", err)
	}
	if err := os.WriteFile(FinishClaimPath(dir, c.TrackerID), data, 0644); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
}

func TestAcquireFinishClaim(t *testing.T) {
	t.Run("a fresh claim is visible to LiveFinishClaimHolder", func(t *testing.T) {
		dir := t.TempDir()
		own, err := AcquireFinishClaim(dir, "proj-100", "sess-1")
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if own.Host == "" || own.PID != os.Getpid() || own.Session != "sess-1" {
			t.Fatalf("claim payload = %+v", own)
		}
		if own.Started == "" || own.Refreshed == "" {
			t.Fatalf("both stamps must be set: %+v", own)
		}

		holder := LiveFinishClaimHolder(dir, "proj-100")
		if holder == nil {
			t.Fatal("holder = nil, want the claim just taken")
		}
		if holder.Host != own.Host || holder.Started != own.Started || holder.Session != "sess-1" {
			t.Errorf("holder = %+v, want %+v", holder, own)
		}
		// A claim on ANOTHER tracker is a different file and must be unaffected.
		if h := LiveFinishClaimHolder(dir, "proj-101"); h != nil {
			t.Errorf("unrelated tracker reads as claimed: %+v", h)
		}
	})

	t.Run("a second acquire is refused with the first holder's details", func(t *testing.T) {
		dir := t.TempDir()
		seedClaim(t, dir, FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711, Session: "sess-a",
			Started:   time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339),
			Refreshed: time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339),
		})

		got, err := AcquireFinishClaim(dir, "proj-100", "sess-b")
		if got != nil {
			t.Errorf("claim = %+v, want nil on a busy tracker", got)
		}
		var busy *FinishClaimBusyError
		if !errors.As(err, &busy) {
			t.Fatalf("err = %v, want *FinishClaimBusyError", err)
		}
		if busy.Holder.Host != "runner" || busy.Holder.PID != 4711 {
			t.Errorf("busy holder = %+v, want the seeded runner/4711 claim", busy.Holder)
		}
		// The message is the whole point of keeping the (unverifiable) pid: a
		// human has to be able to go and look at that machine.
		for _, want := range []string{"runner", "4711", "proj-100"} {
			if !strings.Contains(busy.Error(), want) {
				t.Errorf("busy message %q does not mention %q", busy, want)
			}
		}
		// The incumbent must still be on disk, untouched.
		cur, _ := ReadFinishClaim(dir, "proj-100")
		if cur == nil || cur.Host != "runner" {
			t.Errorf("refused acquire modified the incumbent: %+v", cur)
		}
	})

	// The TTL is the entire liveness rule: a claim nobody refreshed for longer
	// than FinishClaimTTL is void, whatever machine wrote it and whatever its
	// (unverifiable, possibly recycled) pid is doing now.
	t.Run("a claim past the TTL reads as free and can be taken over", func(t *testing.T) {
		dir := t.TempDir()
		seedClaim(t, dir, FinishClaim{
			TrackerID: "proj-100", Host: "mac", PID: os.Getpid(),
			Started:   time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			Refreshed: time.Now().Add(-FinishClaimTTL - time.Minute).UTC().Format(time.RFC3339),
		})

		if h := LiveFinishClaimHolder(dir, "proj-100"); h != nil {
			t.Errorf("holder = %+v, want nil - the claim is past its TTL", h)
		}
		own, err := AcquireFinishClaim(dir, "proj-100", "sess-new")
		if err != nil {
			t.Fatalf("takeover of an expired claim: %v", err)
		}
		cur, _ := ReadFinishClaim(dir, "proj-100")
		if cur == nil || cur.Session != "sess-new" || cur.Started != own.Started {
			t.Fatalf("takeover did not rewrite the claim: %+v", cur)
		}
	})

	// A truncated file from a crashed process must not wall off the run
	// forever - the same rule the worktree lock and the session locks follow.
	t.Run("a corrupt claim reads as free and can be taken over", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(executorRunDir(dir), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(FinishClaimPath(dir, "proj-100"), []byte(`{"tracker_id":"proj-1`), 0644); err != nil {
			t.Fatalf("seed corrupt claim: %v", err)
		}

		if _, err := ReadFinishClaim(dir, "proj-100"); err == nil {
			t.Fatal("fixture is not corrupt: ReadFinishClaim must fail for this test to mean anything")
		}
		if h := LiveFinishClaimHolder(dir, "proj-100"); h != nil {
			t.Errorf("holder = %+v, want nil for a corrupt claim", h)
		}
		if _, err := AcquireFinishClaim(dir, "proj-100", ""); err != nil {
			t.Fatalf("takeover of a corrupt claim: %v", err)
		}
	})

	t.Run("no claim file at all reads as free", func(t *testing.T) {
		dir := t.TempDir()
		c, err := ReadFinishClaim(dir, "proj-100")
		if c != nil || err != nil {
			t.Fatalf("ReadFinishClaim = (%+v, %v), want (nil, nil)", c, err)
		}
		if h := LiveFinishClaimHolder(dir, "proj-100"); h != nil {
			t.Errorf("holder = %+v, want nil", h)
		}
	})
}

func TestRefreshFinishClaim(t *testing.T) {
	// The point of refreshing: a claim survives past the moment it would
	// otherwise have lapsed. Asserted by shrinking the TTL AFTER the refresh, so
	// the same claim that would now be long expired on its original stamp is
	// live on its new one - no sleeping, no wall-clock luck.
	t.Run("refresh moves the stamp and keeps the claim alive past its original TTL", func(t *testing.T) {
		dir := t.TempDir()
		old := time.Now().Add(-5 * time.Minute).UTC().Format(claimTimeLayout)
		own := &FinishClaim{TrackerID: "proj-100", Host: hostname(), PID: os.Getpid(), Started: old, Refreshed: old}
		seedClaim(t, dir, *own)

		if err := RefreshFinishClaim(dir, "proj-100", own); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if own.Refreshed == old {
			t.Error("refresh did not update the caller's copy")
		}
		if own.Started != old {
			t.Errorf("Started moved to %q - it identifies the claim and must not change", own.Started)
		}
		cur, err := ReadFinishClaim(dir, "proj-100")
		if err != nil || cur == nil {
			t.Fatalf("read after refresh: (%+v, %v)", cur, err)
		}
		if cur.Refreshed != own.Refreshed {
			t.Errorf("on-disk refreshed = %q, want %q", cur.Refreshed, own.Refreshed)
		}

		orig := FinishClaimTTL
		t.Cleanup(func() { FinishClaimTTL = orig })
		FinishClaimTTL = 2 * time.Minute // the un-refreshed 5m-old stamp would be void
		if h := LiveFinishClaimHolder(dir, "proj-100"); h == nil {
			t.Error("holder = nil - the refreshed claim must outlive its original stamp")
		}
	})

	// Reviving an EXPIRED claim is the one thing a refresh must not do: every
	// other surface has already reported it free, so a rival may hold it - and
	// the refresh writes by rename, which would silently overwrite their claim
	// and leave two acceptances each believing they own the run.
	t.Run("refusing to revive an expired claim, leaving the file untouched", func(t *testing.T) {
		dir := t.TempDir()
		old := time.Now().Add(-FinishClaimTTL - time.Minute).UTC().Format(claimTimeLayout)
		own := &FinishClaim{TrackerID: "proj-100", Host: hostname(), PID: os.Getpid(), Started: old, Refreshed: old}
		seedClaim(t, dir, *own)
		before := readRaw(t, FinishClaimPath(dir, "proj-100"))

		err := RefreshFinishClaim(dir, "proj-100", own)
		if err == nil {
			t.Fatal("expected an error refreshing an expired claim")
		}
		if !strings.Contains(err.Error(), "TTL") {
			t.Errorf("error %q should explain that the claim is past (or at) its TTL", err)
		}
		if after := readRaw(t, FinishClaimPath(dir, "proj-100")); after != before {
			t.Errorf("claim file changed:\nbefore %s\nafter  %s", before, after)
		}
		// And the way forward is a fresh claim, which is the atomic path.
		if _, err := AcquireFinishClaim(dir, "proj-100", ""); err != nil {
			t.Errorf("re-acquiring the lapsed claim: %v", err)
		}
	})

	// The refusal starts one refresh tick BEFORE the TTL: this is
	// check-then-write, so a claim that passes the check and lapses while the
	// process is descheduled would rename over the rival that took it over.
	t.Run("refusing a claim within one refresh tick of expiring", func(t *testing.T) {
		dir := t.TempDir()
		// Still live (LiveFinishClaimHolder says so), but inside the margin.
		stamp := time.Now().Add(-FinishClaimTTL + FinishClaimRefreshInterval/2).UTC().Format(claimTimeLayout)
		own := &FinishClaim{TrackerID: "proj-100", Host: hostname(), PID: os.Getpid(), Started: stamp, Refreshed: stamp}
		seedClaim(t, dir, *own)
		if LiveFinishClaimHolder(dir, "proj-100") == nil {
			t.Fatal("fixture must still be live - the margin, not the TTL, is what this covers")
		}

		if err := RefreshFinishClaim(dir, "proj-100", own); err == nil {
			t.Fatal("expected a refusal inside the refresh margin")
		}
	})

	t.Run("refreshing somebody else's claim errors and touches nothing", func(t *testing.T) {
		dir := t.TempDir()
		theirs := FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
			Started:   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			Refreshed: time.Now().UTC().Format(time.RFC3339),
		}
		seedClaim(t, dir, theirs)
		before := readRaw(t, FinishClaimPath(dir, "proj-100"))

		ours := &FinishClaim{TrackerID: "proj-100", Host: "mac", PID: os.Getpid(),
			Started: theirs.Started, Refreshed: theirs.Refreshed}
		if err := RefreshFinishClaim(dir, "proj-100", ours); err == nil {
			t.Fatal("expected an error refreshing a claim held by another host")
		}
		// Same host, different Started = a takeover after our claim expired.
		sameHostOlder := &FinishClaim{TrackerID: "proj-100", Host: "runner", PID: 4711,
			Started: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
		if err := RefreshFinishClaim(dir, "proj-100", sameHostOlder); err == nil {
			t.Fatal("expected an error refreshing a claim taken over on the same host")
		}
		if after := readRaw(t, FinishClaimPath(dir, "proj-100")); after != before {
			t.Errorf("claim file changed:\nbefore %s\nafter  %s", before, after)
		}
	})

	t.Run("refreshing a claim that is gone errors", func(t *testing.T) {
		dir := t.TempDir()
		own := &FinishClaim{TrackerID: "proj-100", Host: hostname(), Started: "2026-08-07T10:00:00Z"}
		if err := RefreshFinishClaim(dir, "proj-100", own); err == nil {
			t.Fatal("expected an error refreshing a claim with no file")
		}
	})
}

func TestReleaseFinishClaim(t *testing.T) {
	t.Run("release removes our own claim and is idempotent", func(t *testing.T) {
		dir := t.TempDir()
		own, err := AcquireFinishClaim(dir, "proj-100", "")
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if err := ReleaseFinishClaim(dir, "proj-100", own); err != nil {
			t.Fatalf("release: %v", err)
		}
		if _, err := os.Stat(FinishClaimPath(dir, "proj-100")); !os.IsNotExist(err) {
			t.Errorf("claim file still present after release (stat err = %v)", err)
		}
		if err := ReleaseFinishClaim(dir, "proj-100", own); err != nil {
			t.Errorf("second release must be a no-op, got: %v", err)
		}
		// And the tracker is claimable again.
		if _, err := AcquireFinishClaim(dir, "proj-100", ""); err != nil {
			t.Errorf("re-acquire after release: %v", err)
		}
	})

	t.Run("releasing somebody else's claim errors and leaves it in place", func(t *testing.T) {
		dir := t.TempDir()
		theirs := FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
			Started:   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			Refreshed: time.Now().UTC().Format(time.RFC3339),
		}
		seedClaim(t, dir, theirs)
		before := readRaw(t, FinishClaimPath(dir, "proj-100"))

		ours := &FinishClaim{TrackerID: "proj-100", Host: "mac", Started: theirs.Started}
		if err := ReleaseFinishClaim(dir, "proj-100", ours); err == nil {
			t.Fatal("expected an error releasing another host's claim")
		}
		if after := readRaw(t, FinishClaimPath(dir, "proj-100")); after != before {
			t.Errorf("claim file changed:\nbefore %s\nafter  %s", before, after)
		}
		if h := LiveFinishClaimHolder(dir, "proj-100"); h == nil || h.Host != "runner" {
			t.Errorf("holder = %+v, want the untouched runner claim", h)
		}
	})
}

// TestAcquireFinishClaimAtomicRace hammers both claim paths: two acquirers race
// for the same tracker, once on an empty dir (the link(2)/EEXIST path) and once
// over an already-expired claim (the takeover path, where the winner is decided
// by a rename). Exactly one must win, the loser must get a busy error naming a
// holder, and - the assertion that actually catches a double grant - the claim
// left on disk must be the WINNER'S, not a survivor of two overlapping steals.
// Modelled on TestAcquireWorktreeLockAtomicRace, but the acquirers here need no
// distinct pids: validity is the TTL, not the pid, precisely because a pid means
// nothing across machines.
func TestAcquireFinishClaimAtomicRace(t *testing.T) {
	cases := []struct {
		name string
		seed func(t *testing.T, dir string)
	}{
		{name: "on a free tracker"},
		{
			name: "over an expired claim (the takeover path)",
			seed: func(t *testing.T, dir string) {
				seedClaim(t, dir, FinishClaim{
					TrackerID: "proj-100", Host: "runner", PID: 4711,
					Started:   time.Now().Add(-2 * time.Hour).UTC().Format(claimTimeLayout),
					Refreshed: time.Now().Add(-FinishClaimTTL - time.Minute).UTC().Format(claimTimeLayout),
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 50; i++ {
				dir := t.TempDir()
				if tc.seed != nil {
					tc.seed(t, dir)
				}
				var wg sync.WaitGroup
				claims := make([]*FinishClaim, 2)
				errs := make([]error, 2)
				for j := 0; j < 2; j++ {
					wg.Add(1)
					go func(j int) {
						defer wg.Done()
						claims[j], errs[j] = AcquireFinishClaim(dir, "proj-100", fmt.Sprintf("sess-%d", j))
					}(j)
				}
				wg.Wait()

				winner := -1
				for j, err := range errs {
					if err == nil {
						if winner >= 0 {
							t.Fatalf("iteration %d: two winners - both acquirers hold the run", i)
						}
						winner = j
						if claims[j] == nil {
							t.Fatalf("iteration %d: acquirer %d won but returned a nil claim", i, j)
						}
						continue
					}
					var busy *FinishClaimBusyError
					if !errors.As(err, &busy) {
						t.Fatalf("iteration %d: acquirer %d got a non-busy error: %v", i, j, err)
					}
					if busy.Holder == nil {
						t.Fatalf("iteration %d: busy error carries no holder", i)
					}
				}
				if winner < 0 {
					t.Fatalf("iteration %d: no winner (errs: %v)", i, errs)
				}
				// The file must belong to whoever was TOLD they won - a takeover
				// that deleted the winner's fresh claim would show up here.
				h := LiveFinishClaimHolder(dir, "proj-100")
				if h == nil {
					t.Fatalf("iteration %d: no live holder after the race", i)
				}
				if h.Started != claims[winner].Started || h.Session != claims[winner].Session {
					t.Fatalf("iteration %d: on-disk claim %+v is not the winner's %+v", i, h, claims[winner])
				}
			}
		})
	}
}

// stealExpiredClaim is the takeover primitive, and its whole job is knowing
// what it moved aside. The decision to steal is made BEFORE the rename, so by
// the time it fires the path may hold a rival's fresh claim - deleting that is
// how one run ends up with two acceptances.
func TestStealExpiredClaimOnlyTakesVoidClaims(t *testing.T) {
	t.Run("a live claim is put straight back", func(t *testing.T) {
		dir := t.TempDir()
		live := FinishClaim{
			TrackerID: "proj-100", Host: "mac", PID: 4711,
			Started:   time.Now().UTC().Format(claimTimeLayout),
			Refreshed: time.Now().UTC().Format(claimTimeLayout),
		}
		seedClaim(t, dir, live)
		path := FinishClaimPath(dir, "proj-100")
		before := readRaw(t, path)

		if err := stealExpiredClaim(path); err != nil {
			t.Fatalf("steal over a live claim must succeed by restoring it, got: %v", err)
		}

		if after := readRaw(t, path); after != before {
			t.Errorf("live claim not restored:\nbefore %s\nafter  %s", before, after)
		}
		if h := LiveFinishClaimHolder(dir, "proj-100"); h == nil || h.Started != live.Started {
			t.Errorf("holder = %+v, want the restored live claim", h)
		}
	})

	// Reading the aside file can fail for reasons that say NOTHING about the
	// holder (EACCES, EIO, a stale handle on the shared dir this feature is
	// built for). The steal must not delete on that: what it moved aside may be
	// a rival's live claim. A directory at the claim path is the portable way
	// to make the read fail without a permission trick root would sail through.
	t.Run("what it cannot read is put back, not deleted", func(t *testing.T) {
		dir := t.TempDir()
		path := FinishClaimPath(dir, "proj-100")
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("seed unreadable claim: %v", err)
		}

		err := stealExpiredClaim(path)
		if err == nil {
			t.Fatal("stealing something unreadable must report a failure, not proceed silently")
		}
		// A directory cannot be hard-linked back, so this fixture also covers
		// the restore-failed branch: the file must SURVIVE under its scratch
		// name, and the error must say where it went. Litter a human can put
		// back beats a claim nobody can get back.
		entries, rerr := os.ReadDir(executorRunDir(dir))
		if rerr != nil || len(entries) != 1 {
			t.Fatalf("the unreadable claim was destroyed: entries=%v err=%v", entries, rerr)
		}
		if !strings.Contains(err.Error(), entries[0].Name()) {
			t.Errorf("error %q does not name where the claim was left (%s)", err, entries[0].Name())
		}
	})

	t.Run("an expired claim is taken away", func(t *testing.T) {
		dir := t.TempDir()
		seedClaim(t, dir, FinishClaim{
			TrackerID: "proj-100", Host: "mac", PID: 4711,
			Started:   time.Now().Add(-2 * time.Hour).UTC().Format(claimTimeLayout),
			Refreshed: time.Now().Add(-FinishClaimTTL - time.Minute).UTC().Format(claimTimeLayout),
		})
		path := FinishClaimPath(dir, "proj-100")

		if err := stealExpiredClaim(path); err != nil {
			t.Fatalf("steal of an expired claim: %v", err)
		}

		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expired claim still present (stat err = %v)", err)
		}
	})
}

// A claim that cannot be READ says nothing about its holder - unlike one whose
// content is garbage. The takeover branch DESTROYS what it reads as free, so an
// I/O failure must stop it rather than feed it. (A directory at the claim path
// is the portable way to make a read fail without a permission trick that root
// would sail through.)
func TestAcquireFinishClaimRefusesOnUnreadableClaim(t *testing.T) {
	dir := t.TempDir()
	path := FinishClaimPath(dir, "proj-100")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("seed unreadable claim: %v", err)
	}

	_, err := AcquireFinishClaim(dir, "proj-100", "")
	if err == nil {
		t.Fatal("expected an error when the claim cannot be read")
	}
	var busy *FinishClaimBusyError
	if errors.As(err, &busy) {
		t.Errorf("err = %v, want a read failure, not a busy report", err)
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Errorf("the unreadable claim was destroyed: %v", serr)
	}
}

// The tracker id becomes a file name under .executor/, so it is validated where
// it is written - the repo's "identifiers are PATH COMPONENTS" rule. Without
// this, `pm finish claim ../../x` writes outside the project dir and the
// matching release is an arbitrary unlink.
func TestFinishClaimRejectsUnsafeTrackerIDs(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"../escape", "a/b", "", ".."} {
		if _, err := AcquireFinishClaim(dir, bad, ""); err == nil {
			t.Errorf("AcquireFinishClaim(%q) = nil error, want a rejection", bad)
		}
		own := &FinishClaim{TrackerID: bad, Host: hostname()}
		if err := ReleaseFinishClaim(dir, bad, own); err == nil {
			t.Errorf("ReleaseFinishClaim(%q) = nil error, want a rejection", bad)
		}
		if err := RefreshFinishClaim(dir, bad, own); err == nil {
			t.Errorf("RefreshFinishClaim(%q) = nil error, want a rejection", bad)
		}
	}
	// Nothing was created on the way out.
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		t.Errorf("a rejected id left something behind: %v", entries)
	}
}

// Started is half the claim's identity, so two claims taken back to back on one
// host must not be interchangeable - at second granularity they were, and a
// stale copy of a released claim would then release its successor.
func TestFinishClaimStartedIsUniquePerClaim(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireFinishClaim(dir, "proj-100", "sess-1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := ReleaseFinishClaim(dir, "proj-100", first); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := AcquireFinishClaim(dir, "proj-100", "sess-2")
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if first.Started == second.Started {
		t.Fatalf("both claims stamped %q - a stale claim would pass the ownership test", first.Started)
	}
	if err := ReleaseFinishClaim(dir, "proj-100", first); err == nil {
		t.Error("the released claim's copy must not be able to release its successor")
	}
	if h := LiveFinishClaimHolder(dir, "proj-100"); h == nil || h.Session != "sess-2" {
		t.Errorf("holder = %+v, want the second claim intact", h)
	}
}

// The TTL knob has to be a var, not a const: a test (here, and later an
// acceptance-side one driving a refresher loop) has to be able to shrink it.
// The same claim file must read live under the default TTL and expired under a
// shortened one - nothing else about the claim changes.
func TestFinishClaimTTLIsTunable(t *testing.T) {
	dir := t.TempDir()
	seedClaim(t, dir, FinishClaim{
		TrackerID: "proj-100", Host: "mac", PID: os.Getpid(),
		Started:   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		Refreshed: time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339),
	})

	if h := LiveFinishClaimHolder(dir, "proj-100"); h == nil {
		t.Fatal("holder = nil under the default 10m TTL for a claim refreshed 2s ago")
	}

	orig := FinishClaimTTL
	t.Cleanup(func() { FinishClaimTTL = orig })
	FinishClaimTTL = time.Second

	if h := LiveFinishClaimHolder(dir, "proj-100"); h != nil {
		t.Errorf("holder = %+v under a 1s TTL, want nil - the shortened TTL did not take effect", h)
	}
	if _, err := AcquireFinishClaim(dir, "proj-100", ""); err != nil {
		t.Errorf("takeover under the shortened TTL: %v", err)
	}
}

func readRaw(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// LiveFinishClaims is the board's per-tick enumeration of hand-driven
// acceptances, so it must return exactly the claims that are live - and key
// each under the tracker its own content names, never under a guess parsed
// out of the file name.
func TestLiveFinishClaims(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Format(claimTimeLayout)
	old := time.Now().Add(-FinishClaimTTL - time.Minute).UTC().Format(claimTimeLayout)

	seedClaim(t, dir, FinishClaim{TrackerID: "proj-1", Host: "mac", PID: 1, Session: "sess-live", Started: now, Refreshed: now})
	seedClaim(t, dir, FinishClaim{TrackerID: "proj-2", Host: "mac", PID: 2, Started: old, Refreshed: old})
	if err := os.WriteFile(FinishClaimPath(dir, "proj-3"), []byte("{garbage"), 0644); err != nil {
		t.Fatal(err)
	}
	// Content naming ANOTHER tracker than the file it sits in: skipped, the
	// same self-identification rule readRunStatesDir applies to run-states.
	stray, err := json.Marshal(FinishClaim{TrackerID: "somewhere-else", Host: "mac", Started: now, Refreshed: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(FinishClaimPath(dir, "proj-4"), stray, 0644); err != nil {
		t.Fatal(err)
	}
	// A dot in a task id is legal, and `x.finish`'s claim file ends in
	// `.finish.finish.claim` - it must come back under its own id.
	seedClaim(t, dir, FinishClaim{TrackerID: "x.finish", Host: "mac", PID: 3, Started: now, Refreshed: now})

	got := LiveFinishClaims(dir)
	if len(got) != 2 {
		t.Fatalf("LiveFinishClaims returned %d claims (%v), want exactly proj-1 and x.finish", len(got), got)
	}
	if c := got["proj-1"]; c == nil || c.Session != "sess-live" {
		t.Errorf("proj-1 = %+v, want the live claim with its session", c)
	}
	if c := got["x.finish"]; c == nil {
		t.Errorf("the dotted tracker id went missing: %v", got)
	}

	// No .executor dir at all reads as no claims, never as an error.
	if n := len(LiveFinishClaims(t.TempDir())); n != 0 {
		t.Errorf("a project without an .executor dir returned %d claims", n)
	}
}
