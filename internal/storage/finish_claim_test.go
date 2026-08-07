package storage

import (
	"encoding/json"
	"errors"
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
	t.Run("refresh moves the stamp and keeps the claim alive past the TTL", func(t *testing.T) {
		dir := t.TempDir()
		// Claim taken well over a TTL ago: only a refresh can keep it valid.
		old := time.Now().Add(-FinishClaimTTL - time.Minute).UTC().Format(time.RFC3339)
		own := &FinishClaim{TrackerID: "proj-100", Host: hostname(), PID: os.Getpid(), Started: old, Refreshed: old}
		seedClaim(t, dir, *own)
		if h := LiveFinishClaimHolder(dir, "proj-100"); h != nil {
			t.Fatal("fixture must start expired")
		}

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
		if h := LiveFinishClaimHolder(dir, "proj-100"); h == nil {
			t.Error("holder = nil after a refresh - the claim must be live again")
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

// TestAcquireFinishClaimAtomicRace hammers the link(2) claim: two acquirers race
// for the same tracker; exactly one must win and the loser must get a busy error
// naming a holder - never two winners, never two losers. Modelled on
// TestAcquireWorktreeLockAtomicRace, but the acquirers here need no distinct
// pids: validity is the TTL, not the pid, precisely because a pid means nothing
// across machines.
func TestAcquireFinishClaimAtomicRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		dir := t.TempDir()
		var wg sync.WaitGroup
		claims := make([]*FinishClaim, 2)
		errs := make([]error, 2)
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func(j int) {
				defer wg.Done()
				claims[j], errs[j] = AcquireFinishClaim(dir, "proj-100", "sess")
			}(j)
		}
		wg.Wait()

		wins := 0
		for j, err := range errs {
			if err == nil {
				wins++
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
		if wins != 1 {
			t.Fatalf("iteration %d: expected exactly 1 winner, got %d (errs: %v)", i, wins, errs)
		}
		// The winner's claim is the one on disk, whole and parseable.
		if h := LiveFinishClaimHolder(dir, "proj-100"); h == nil {
			t.Fatalf("iteration %d: no live holder after the race", i)
		}
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
