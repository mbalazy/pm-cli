package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	// Never a nil slice: cobra falls back to os.Args[1:] when args is nil, which
	// would feed the test binary's own flags to the command (the same trap
	// context_test.go guards against).
	cmd.SetArgs(append([]string{}, args...))
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	return out.String(), err
}

// finishStore is tempStore plus the tracker every `pm finish` subcommand
// resolves: a claim on a tracker that does not exist protects nothing, so the
// commands refuse an id that names no task.
func finishStore(t *testing.T) (*storage.Store, string) {
	t.Helper()
	store, slug := tempStore(t)
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-100", Title: "Tracker", Status: storage.StatusDoing}, "")
	return store, slug
}

// seedFinishClaim plants a claim in the project's data dir as if another
// machine (or another session) had taken it - the public API only ever writes
// THIS host's claim, so the fixture writes the file itself. Every field of c is
// preserved: a helper that quietly substituted its own pid would have later
// tests asserting the fixture instead of the code.
func seedFinishClaim(t *testing.T, store *storage.Store, slug string, c storage.FinishClaim) {
	t.Helper()
	dir := store.ProjectDir(slug)
	if err := os.MkdirAll(filepath.Dir(storage.FinishClaimPath(dir, c.TrackerID)), 0755); err != nil {
		t.Fatalf("seed: mkdir: %v", err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("seed: marshal claim: %v", err)
	}
	if err := os.WriteFile(storage.FinishClaimPath(dir, c.TrackerID), data, 0644); err != nil {
		t.Fatalf("seed: write claim: %v", err)
	}
}

func TestFinishClaimCmd(t *testing.T) {
	t.Run("claims a free tracker and reports where the claim lives", func(t *testing.T) {
		store, slug := finishStore(t)
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
		store, slug := finishStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
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
		store, slug := finishStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
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
	// The claim it took is released by NAMING it - `pm finish claim` prints the
	// stamp for exactly this.
	t.Run("releases a claim named with --started, and says so when there is none", func(t *testing.T) {
		store, slug := finishStore(t)
		if _, err := runFinishCmd(t, store, "claim", "proj-100", "--project", slug); err != nil {
			t.Fatalf("claim: %v", err)
		}
		mine, _ := storage.ReadFinishClaim(store.ProjectDir(slug), "proj-100")
		if mine == nil {
			t.Fatal("claim not on disk")
		}

		out, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug, "--started", mine.Started)
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

	t.Run("--session identifies the claim just as well", func(t *testing.T) {
		store, slug := finishStore(t)
		if _, err := runFinishCmd(t, store, "claim", "proj-100", "--project", slug, "--session", "sess-1"); err != nil {
			t.Fatalf("claim: %v", err)
		}
		if _, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug, "--session", "sess-1"); err != nil {
			t.Fatalf("release by session: %v", err)
		}
		if h := storage.LiveFinishClaimHolder(store.ProjectDir(slug), "proj-100"); h != nil {
			t.Errorf("holder = %+v after release, want nil", h)
		}
	})

	// The hole this closes: `pm finish claim` exits immediately, so a release
	// that lifted whatever claim it found would let the SECOND session on the
	// same machine delete the first's live claim and then take the run - the
	// exact collision the lock exists to stop. Two CC sessions on one box is the
	// ordinary case, not an exotic one.
	t.Run("refuses to lift a live claim the caller cannot name", func(t *testing.T) {
		store, slug := finishStore(t)
		if _, err := runFinishCmd(t, store, "claim", "proj-100", "--project", slug, "--session", "sess-a"); err != nil {
			t.Fatalf("claim: %v", err)
		}

		out, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug)
		if err == nil {
			t.Fatalf("release with no identifier must fail (out: %s)", out)
		}
		if _, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug, "--started", "2020-01-01T00:00:00Z"); err == nil {
			t.Fatal("release with a WRONG identifier must fail")
		}
		if h := storage.LiveFinishClaimHolder(store.ProjectDir(slug), "proj-100"); h == nil || h.Session != "sess-a" {
			t.Fatalf("holder = %+v, want the untouched live claim", h)
		}
	})

	// A claim belongs to the machine that took it. Lifting another machine's
	// live claim would hand its run to a third session.
	t.Run("refuses to release another host's live claim and leaves it in place", func(t *testing.T) {
		store, slug := finishStore(t)
		theirs := storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
			Started:   time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			Refreshed: time.Now().UTC().Format(time.RFC3339),
		}
		seedFinishClaim(t, store, slug, theirs)

		_, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug)
		if err == nil {
			t.Fatal("releasing another host's claim must fail")
		}
		if !strings.Contains(err.Error(), "runner") {
			t.Errorf("error %q does not name the holding host", err)
		}
		// Even knowing its stamp: it is not this machine's to lift.
		if _, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug, "--started", theirs.Started); err == nil {
			t.Error("another host's live claim must stay theirs even when named")
		}
		if h := storage.LiveFinishClaimHolder(store.ProjectDir(slug), "proj-100"); h == nil || h.Host != "runner" {
			t.Errorf("holder = %+v, want the untouched runner claim", h)
		}
	})

	// An expired claim reads as free to every other surface, so clearing it
	// takes nothing from anybody - and needs no identifier, since whoever left
	// it behind is by definition not coming back to name it.
	t.Run("clears an expired claim without an identifier", func(t *testing.T) {
		store, slug := finishStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
			Started:   time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339),
			Refreshed: time.Now().Add(-storage.FinishClaimTTL - time.Hour).UTC().Format(time.RFC3339),
		})

		out, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug)
		if err != nil {
			t.Fatalf("clearing an expired claim: %v", err)
		}
		if !strings.Contains(out, "expired") {
			t.Errorf("output %q should say the claim had already expired", out)
		}
		if _, serr := os.Stat(storage.FinishClaimPath(store.ProjectDir(slug), "proj-100")); !os.IsNotExist(serr) {
			t.Errorf("expired claim still on disk (stat err = %v)", serr)
		}
	})

	// Everything else treats a corrupt claim as free; release must not be the
	// one command that turns it into a dead end demanding a manual rm.
	t.Run("an unreadable claim file is not an error", func(t *testing.T) {
		store, slug := finishStore(t)
		path := storage.FinishClaimPath(store.ProjectDir(slug), "proj-100")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(`{"tracker_id":"proj-1`), 0644); err != nil {
			t.Fatalf("seed corrupt claim: %v", err)
		}

		out, err := runFinishCmd(t, store, "release", "proj-100", "--project", slug)
		if err != nil {
			t.Fatalf("a corrupt claim must not fail the release, got: %v", err)
		}
		if !strings.Contains(out, "unreadable") {
			t.Errorf("output %q should explain the claim file is unreadable", out)
		}
	})
}

func TestFinishStatusCmd(t *testing.T) {
	t.Run("free when nothing holds it", func(t *testing.T) {
		store, slug := finishStore(t)
		out, err := runFinishCmd(t, store, "status", "proj-100", "--project", slug)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if !strings.Contains(out, "free") {
			t.Errorf("output %q, want a free report", out)
		}
	})

	t.Run("names the holder, when it started and how fresh it is", func(t *testing.T) {
		store, slug := finishStore(t)
		started := time.Now().Add(-7 * time.Minute).UTC().Format(time.RFC3339)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
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
		store, slug := finishStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
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

// `pm finish` has to be reachable from the root command - a subcommand nobody
// registered is a subcommand nobody can run.
func TestFinishIsRegisteredOnRoot(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "finish" {
			return
		}
	}
	t.Fatal("`finish` is not registered on the root command")
}

// The tracker id becomes a file name, and release UNLINKS that file, so an id
// carrying a path separator must be refused before either touches the disk.
func TestFinishRejectsUnsafeTrackerID(t *testing.T) {
	store, slug := finishStore(t)
	// ../../escape is the one that actually leaves the project dir: the claim
	// path is <root>/<slug>/.executor/<id>.finish.claim, so a single ".." only
	// climbs to <root>/<slug>. Both must be refused; the escape is what the
	// stat below proves.
	for _, sub := range []string{"claim", "release", "status"} {
		for _, id := range []string{"../escape", "../../escape", "a/b"} {
			if _, err := runFinishCmd(t, store, sub, id, "--project", slug); err == nil {
				t.Errorf("`pm finish %s %s` was accepted", sub, id)
			}
		}
	}
	for _, stray := range []string{
		filepath.Join(store.ProjectDir(slug), "escape.finish.claim"),
		filepath.Join(store.RootDir(), "escape.finish.claim"),
	} {
		if _, err := os.Stat(stray); !os.IsNotExist(err) {
			t.Errorf("a rejected id wrote %s (stat err = %v)", stray, err)
		}
	}
}

// A tracker nobody has heard of must not read as a successful claim: a typo is
// the likeliest way two sessions each get a green light on the same real run.
func TestFinishRefusesAnUnknownTracker(t *testing.T) {
	store, slug := finishStore(t)
	out, err := runFinishCmd(t, store, "claim", "proj-1OO", "--project", slug)
	if err == nil {
		t.Fatalf("claiming a tracker that does not exist must fail (out: %s)", out)
	}
	if !strings.Contains(err.Error(), "proj-1OO") {
		t.Errorf("error %q does not name the id that was not found", err)
	}
	if _, serr := os.Stat(storage.FinishClaimPath(store.ProjectDir(slug), "proj-1OO")); !os.IsNotExist(serr) {
		t.Error("a refused claim still wrote a claim file")
	}
}

// status must not answer "free" where claim will refuse: an I/O failure says
// nothing about the holder, so the two surfaces have to agree.
func TestFinishStatusReportsAnUnreadableClaimAsAFailure(t *testing.T) {
	store, slug := finishStore(t)
	path := storage.FinishClaimPath(store.ProjectDir(slug), "proj-100")
	if err := os.MkdirAll(path, 0755); err != nil { // a directory: readable path, unreadable file
		t.Fatalf("seed unreadable claim: %v", err)
	}

	out, err := runFinishCmd(t, store, "status", "proj-100", "--project", slug)
	if err == nil {
		t.Fatalf("status must fail on an unreadable claim (out: %s)", out)
	}
	if strings.Contains(out, "free") {
		t.Errorf("output %q reports free for a claim `pm finish claim` will refuse", out)
	}
}

// An acceptance seldom stands in the project's checkout (batch-finish-auto
// works in worktrees under /tmp), so with no --project and a cwd that is no
// project dir the tracker id's own prefix must name the project.
func TestFinishClaimCmdResolvesProjectFromTaskID(t *testing.T) {
	store, slug := finishStore(t)

	t.Run("prefix of the tracker id names the project", func(t *testing.T) {
		out, err := runFinishCmd(t, store, "claim", "proj-100", "--session", "sess-1")
		if err != nil {
			t.Fatalf("claim without --project outside a project dir: %v\n%s", err, out)
		}
		if _, err := os.Stat(storage.FinishClaimPath(store.ProjectDir(slug), "proj-100")); err != nil {
			t.Fatalf("claim file not written under project %s: %v", slug, err)
		}
	})

	t.Run("an id matching no project still says no project", func(t *testing.T) {
		_, err := runFinishCmd(t, store, "status", "other-7")
		if err == nil || !strings.Contains(err.Error(), "no project") {
			t.Fatalf("want a 'no project' error, got %v", err)
		}
	})

	t.Run("two projects with one prefix are refused, not guessed", func(t *testing.T) {
		if err := store.CreateProject("twin", &storage.Project{Name: "Twin", Path: t.TempDir(), Prefix: "proj"}); err != nil {
			t.Fatal(err)
		}
		_, err := runFinishCmd(t, store, "status", "proj-100")
		if err == nil || !strings.Contains(err.Error(), "--project") {
			t.Fatalf("want an ambiguity error naming --project, got %v", err)
		}
	})
}
