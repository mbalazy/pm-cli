package cmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// runFinishCmd executes `pm finish ...` through the real parent command, so the
// --project persistent flag resolves exactly as it does in production (a
// subcommand built on its own would never see it).
func runFinishCmd(t *testing.T, store storage.TaskStore, args ...string) (string, error) {
	t.Helper()
	cmd := newFinishCmd(store)
	var out strings.Builder
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

// seedFinishClaim writes a claim into the project's data dir as if another
// machine had taken it.
func seedFinishClaim(t *testing.T, store *storage.Store, slug string, c storage.FinishClaim) {
	t.Helper()
	dir := store.ProjectDir(slug)
	if _, err := storage.AcquireFinishClaim(dir, c.TrackerID, ""); err != nil {
		t.Fatalf("seed: acquire %s: %v", c.TrackerID, err)
	}
	// Overwrite the just-created file with the claim we actually want (the
	// public API only ever writes THIS host's claim).
	data := `{"tracker_id":"` + c.TrackerID + `","host":"` + c.Host + `","pid":4711` +
		`,"started":"` + c.Started + `","refreshed":"` + c.Refreshed + `"}`
	if err := os.WriteFile(storage.FinishClaimPath(dir, c.TrackerID), []byte(data), 0644); err != nil {
		t.Fatalf("seed: write claim: %v", err)
	}
}

func TestFinishClaimCmd(t *testing.T) {
	t.Run("claims a free tracker and reports where the claim lives", func(t *testing.T) {
		store, slug := tempStore(t)
		out, err := runFinishCmd(t, store, "claim", "proj-100", "--project", slug, "--session", "sess-1")
		if err != nil {
			t.Fatalf("claim: %v (out: %s)", err, out)
		}
		if !strings.Contains(out, "claimed proj-100") {
			t.Errorf("output %q does not confirm the claim", out)
		}
		claim, rerr := storage.ReadFinishClaim(store.ProjectDir(slug), "proj-100")
		if rerr != nil || claim == nil {
			t.Fatalf("claim not on disk: (%+v, %v)", claim, rerr)
		}
		if claim.Session != "sess-1" {
			t.Errorf("session = %q, want sess-1 (--session must reach the claim)", claim.Session)
		}
	})

	// The refusal is the whole point: a second acceptance session must be told
	// which machine to go and look at, and must exit non-zero so a script stops.
	t.Run("refuses a tracker somebody else holds, naming host and pid", func(t *testing.T) {
		store, slug := tempStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner",
			Started:   time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339),
			Refreshed: time.Now().UTC().Format(time.RFC3339),
		})

		_, err := runFinishCmd(t, store, "claim", "proj-100", "--project", slug)
		if err == nil {
			t.Fatal("claiming a held tracker must fail")
		}
		for _, want := range []string{"runner", "4711", "proj-100"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	})

	t.Run("takes over a claim past its TTL", func(t *testing.T) {
		store, slug := tempStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner",
			Started:   time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			Refreshed: time.Now().Add(-storage.FinishClaimTTL - time.Minute).UTC().Format(time.RFC3339),
		})

		if _, err := runFinishCmd(t, store, "claim", "proj-100", "--project", slug); err != nil {
			t.Fatalf("takeover of an expired claim must succeed, got: %v", err)
		}
		claim, _ := storage.ReadFinishClaim(store.ProjectDir(slug), "proj-100")
		if claim == nil || claim.Host != storage.Hostname() {
			t.Fatalf("claim after takeover = %+v, want this host", claim)
		}
	})
}

func TestFinishReleaseCmd(t *testing.T) {
	t.Run("releases our own claim, and says so when there is none", func(t *testing.T) {
		store, slug := tempStore(t)
		if _, err := runFinishCmd(t, store, "claim", "proj-100", "--project", slug); err != nil {
			t.Fatalf("claim: %v", err)
		}

		out, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug)
		if err != nil {
			t.Fatalf("release: %v (out: %s)", err, out)
		}
		if !strings.Contains(out, "released") {
			t.Errorf("output %q does not confirm the release", out)
		}
		if h := storage.LiveFinishClaimHolder(store.ProjectDir(slug), "proj-100"); h != nil {
			t.Errorf("holder = %+v after release, want nil", h)
		}

		out, err = runFinishCmd(t, store, "release", "proj-100", "--project", slug)
		if err != nil {
			t.Fatalf("releasing nothing must not be an error, got: %v", err)
		}
		if !strings.Contains(out, "nothing to release") {
			t.Errorf("output %q does not report an absent claim", out)
		}
	})

	// A claim belongs to the machine that took it. Lifting another machine's
	// claim would hand its run to a third session - which is the collision this
	// whole lock exists to stop.
	t.Run("refuses to release another host's claim and leaves it in place", func(t *testing.T) {
		store, slug := tempStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner",
			Started:   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			Refreshed: time.Now().UTC().Format(time.RFC3339),
		})

		_, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug)
		if err == nil {
			t.Fatal("releasing another host's claim must fail")
		}
		if !strings.Contains(err.Error(), "runner") {
			t.Errorf("error %q does not name the holding host", err)
		}
		if h := storage.LiveFinishClaimHolder(store.ProjectDir(slug), "proj-100"); h == nil || h.Host != "runner" {
			t.Errorf("holder = %+v, want the untouched runner claim", h)
		}
	})
}

func TestFinishStatusCmd(t *testing.T) {
	t.Run("free when nothing holds it", func(t *testing.T) {
		store, slug := tempStore(t)
		out, err := runFinishCmd(t, store, "status", "proj-100", "--project", slug)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if !strings.Contains(out, "free") {
			t.Errorf("output %q, want a free report", out)
		}
	})

	t.Run("names the holder, when it started and how fresh it is", func(t *testing.T) {
		store, slug := tempStore(t)
		started := time.Now().Add(-7 * time.Minute).UTC().Format(time.RFC3339)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner",
			Started:   started,
			Refreshed: time.Now().UTC().Format(time.RFC3339),
		})

		out, err := runFinishCmd(t, store, "status", "proj-100", "--project", slug)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		for _, want := range []string{"claimed by runner", "4711", started, "refreshed"} {
			if !strings.Contains(out, want) {
				t.Errorf("output %q does not contain %q", out, want)
			}
		}
	})

	// An expired claim is FREE - reporting it as held would send a human to a
	// machine where nothing is running.
	t.Run("an expired claim reads as free, with the stale holder for context", func(t *testing.T) {
		store, slug := tempStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner",
			Started:   time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339),
			Refreshed: time.Now().Add(-storage.FinishClaimTTL - time.Hour).UTC().Format(time.RFC3339),
		})

		out, err := runFinishCmd(t, store, "status", "proj-100", "--project", slug)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if !strings.Contains(out, "free") || !strings.Contains(out, "expired") {
			t.Errorf("output %q, want a free/expired report", out)
		}
		if !strings.Contains(out, "runner") {
			t.Errorf("output %q drops the stale holder - it is the only hint where the run was", out)
		}
	})
}

// The bare `pm finish` is a skeleton until pm-cli-100-4 adds the
// worker-spawning form: it prints help rather than doing anything, and a
// positional tracker is not silently swallowed.
func TestFinishBareCommandIsASkeleton(t *testing.T) {
	store, _ := tempStore(t)

	out, err := runFinishCmd(t, store)
	if err != nil {
		t.Fatalf("bare `pm finish`: %v", err)
	}
	for _, want := range []string{"claim", "release", "status"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output %q does not list the %q subcommand", out, want)
		}
	}

	if _, err := runFinishCmd(t, store, "proj-100"); err == nil {
		t.Error("`pm finish <tracker>` must not be accepted yet (it lands in pm-cli-100-4)")
	}
}
