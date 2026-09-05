package runctl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// newStore builds a temp store: project "test" (prefix t) whose path is a
// real directory, a leaf task t-1, a tracker t-2 with the child t-2-1.
func newStore(t *testing.T) (*storage.Store, string) {
	t.Helper()
	root := t.TempDir()
	repo := t.TempDir()
	store := &storage.Store{Root: root}
	projDir := filepath.Join(root, "test")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteProject(filepath.Join(projDir, "project.yaml"), &storage.Project{Name: "Test", Prefix: "t", Path: repo}); err != nil {
		t.Fatal(err)
	}
	mk := func(id, parent string) {
		task := &storage.Task{
			Meta:     storage.TaskMeta{ID: id, Title: "Task " + id, Status: storage.StatusDoing, Parent: parent, Created: "2026-01-01", Updated: "2026-01-01T10:00:00Z"},
			FilePath: filepath.Join(projDir, id+".md"), Project: "test",
		}
		if err := storage.WriteTask(task); err != nil {
			t.Fatal(err)
		}
	}
	mk("t-1", "")
	mk("t-2", "")
	mk("t-2-1", "t-2")
	return store, projDir
}

// fakePM is a `pm` that records its argv and the launch source, then sleeps
// so a kill has something to signal.
func fakePM(t *testing.T) (exe, out string) {
	t.Helper()
	dir := t.TempDir()
	out = filepath.Join(dir, "argv.txt")
	exe = filepath.Join(dir, "pm")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$FAKE_PM_OUT\"\nprintf 'source=%s\\n' \"$PM_LAUNCH_SOURCE\" >> \"$FAKE_PM_OUT\"\nsleep 30\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PM_OUT", out)
	return exe, out
}

func immediate(_ time.Duration, fn func()) { fn() }

func newController(t *testing.T, store *storage.Store, exe string) *Controller {
	t.Helper()
	return New(store, Options{Exe: exe, Session: "cockpit-testhost", After: immediate})
}

func TestPlan(t *testing.T) {
	store, _ := newStore(t)
	c := newController(t, store, "/bin/false")

	t.Run("resume_run: run-epic for a tracker, work for a leaf, flags honoured only where they apply", func(t *testing.T) {
		p, err := c.Plan("test", "t-2", ActionResumeRun, Flags{Yolo: true, Additional: true})
		if err != nil {
			t.Fatal(err)
		}
		// No worktree slots configured: --additional is dropped and said so.
		if strings.Join(p.Argv, " ") != "run-epic test t-2 --yolo" || p.AdditionalAvail || p.Kind != storage.RunKindEpic {
			t.Fatalf("plan = %+v", p)
		}
		if p.Cwd == "" || !strings.HasSuffix(p.Log, "t-2.log") {
			t.Fatalf("plan = %+v", p)
		}
		// Not a git checkout: a warning, never a block.
		if len(p.Warnings) == 0 || !strings.Contains(p.Warnings[0], "not a git repo") {
			t.Fatalf("warnings = %v", p.Warnings)
		}
		leaf, err := c.Plan("test", "t-1", ActionResumeRun, Flags{})
		if err != nil || strings.Join(leaf.Argv, " ") != "work test t-1" || leaf.Kind != storage.RunKindWork {
			t.Fatalf("leaf plan = %+v %v", leaf, err)
		}
	})
	t.Run("rerun_finish never carries --sim", func(t *testing.T) {
		p, err := c.Plan("test", "t-2", ActionRerunFinish, Flags{Yolo: true})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(p.Argv, " ") != "finish t-2 --project test --no-sim" || !strings.HasSuffix(p.Log, "t-2.finish.log") {
			t.Fatalf("plan = %+v", p)
		}
	})
	t.Run("claim carries the session; kill with no run says so", func(t *testing.T) {
		p, err := c.Plan("test", "t-2", ActionClaim, Flags{})
		if err != nil || p.Session != "cockpit-testhost" || len(p.Argv) != 0 {
			t.Fatalf("plan = %+v %v", p, err)
		}
		k, err := c.Plan("test", "t-2", ActionKill, Flags{})
		if err != nil || k.Target != "nothing is running" || k.PID != 0 {
			t.Fatalf("kill plan = %+v %v", k, err)
		}
	})
	t.Run("unknown action 400-ish, missing task/project 404-ish", func(t *testing.T) {
		var input *InputError
		if _, err := c.Plan("test", "t-2", Action("dance"), Flags{}); !errors.As(err, &input) {
			t.Fatalf("err = %v", err)
		}
		var nf *NotFoundError
		if _, err := c.Plan("test", "t-9", ActionKill, Flags{}); !errors.As(err, &nf) {
			t.Fatalf("err = %v", err)
		}
		if _, err := c.Plan("nope", "t-2", ActionKill, Flags{}); !errors.As(err, &nf) {
			t.Fatalf("err = %v", err)
		}
	})
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "source=") {
			return string(b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
	return ""
}

func TestSpawnAndKill(t *testing.T) {
	store, projDir := newStore(t)
	exe, out := fakePM(t)
	c := newController(t, store, exe)

	p, err := c.Plan("test", "t-2", ActionResumeRun, Flags{Yolo: true})
	if err != nil {
		t.Fatal(err)
	}
	started, err := c.Spawn(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-started.PID, syscall.SIGKILL) })
	if started.PID <= 0 || started.Kind != "run-epic" || strings.Join(started.Argv, " ") != "pm run-epic test t-2 --yolo" {
		t.Fatalf("started = %+v", started)
	}
	// The fake ran with exactly the plan's argv and the cockpit source.
	got := waitFile(t, out)
	if !strings.HasPrefix(got, "run-epic test t-2 --yolo\n") || !strings.Contains(got, "source=cockpit") {
		t.Fatalf("fake pm saw:\n%s", got)
	}
	// The run-state is seeded so the queue shows the run at once.
	st, err := storage.ReadRunState(projDir, "t-2")
	if err != nil || st.PID != started.PID || st.Status != storage.RunStatusRunning || st.Kind != "run-epic" {
		t.Fatalf("seed = %+v %v", st, err)
	}
	if !st.IsLive() {
		t.Fatal("the seeded run must read as live while the fake sleeps")
	}
	if _, err := os.Stat(started.Log); err != nil {
		t.Fatal("the log must exist")
	}
	// The kill plan names the live run.
	kp, err := c.Plan("test", "t-2", ActionKill, Flags{})
	if err != nil || kp.PID != started.PID || !strings.HasPrefix(kp.Target, "run-epic pid ") {
		t.Fatalf("kill plan = %+v %v", kp, err)
	}

	// Kill: SIGTERM the group (the escalation runs at once here), reconcile,
	// journal with the source, park the in-flight task.
	res, err := c.Kill("test", "t-2")
	if err != nil {
		t.Fatal(err)
	}
	if res.PID != started.PID || res.Kind != "run-epic" || res.Parked != "t-2" {
		t.Fatalf("kill = %+v", res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && storage.ProcessAlive(started.PID) {
		time.Sleep(10 * time.Millisecond)
	}
	if storage.ProcessAlive(started.PID) {
		t.Fatal("the fake is still alive after the kill")
	}
	st, _ = storage.ReadRunState(projDir, "t-2")
	if st.Status != storage.RunStatusFailed || !strings.Contains(st.Error, "cockpit") {
		t.Fatalf("state after kill = %+v", st)
	}
	entries, _ := storage.ReadJournal(projDir)
	var killed *storage.JournalEntry
	for i := range entries {
		if entries[i].Event == storage.JournalEventKilled {
			killed = &entries[i]
		}
	}
	if killed == nil || killed.Source != Source || killed.PID != started.PID {
		t.Fatalf("journal = %+v", entries)
	}
	task, _ := store.FindTaskExact("test", "t-2")
	if task.Meta.Status != storage.StatusWaiting {
		t.Fatalf("the in-flight task must be parked on waiting, got %s", task.Meta.Status)
	}

	// A second kill finds a dead pid: a StaleRunError, nothing signalled.
	var stale *storage.StaleRunError
	if _, err := c.Kill("test", "t-2"); !errors.As(err, &stale) {
		t.Fatalf("second kill = %v", err)
	}
}

func TestKillStaleAndNoRun(t *testing.T) {
	store, projDir := newStore(t)
	c := newController(t, store, "/bin/false")
	var noRun *NoRunError
	if _, err := c.Kill("test", "t-1"); !errors.As(err, &noRun) {
		t.Fatalf("no run = %v", err)
	}
	// A run-state whose pid is long gone: stale, reconciled, not signalled.
	if err := storage.WriteRunState(projDir, &storage.RunState{TaskID: "t-1", Project: "test", Kind: "work", Status: storage.RunStatusRunning, PID: 999999, Started: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	var stale *storage.StaleRunError
	if _, err := c.Kill("test", "t-1"); !errors.As(err, &stale) {
		t.Fatalf("stale = %v", err)
	}
	st, _ := storage.ReadRunState(projDir, "t-1")
	if st.Status != storage.RunStatusFailed || !strings.Contains(st.Error, "already gone") {
		t.Fatalf("state = %+v", st)
	}
}

func TestClaimAndRelease(t *testing.T) {
	store, projDir := newStore(t)
	c := newController(t, store, "/bin/false")
	old := ClaimHeartbeat
	ClaimHeartbeat = 20 * time.Millisecond
	t.Cleanup(func() { ClaimHeartbeat = old })

	res, err := c.Claim("test", "t-2")
	if err != nil || res.Refreshed || res.Claim.Session != "cockpit-testhost" {
		t.Fatalf("claim = %+v %v", res, err)
	}
	if len(c.Held()) != 1 {
		t.Fatalf("held = %v", c.Held())
	}
	if !storage.CockpitOwnsClaim(res.Claim) {
		// The attention row reads this to offer release_claim.
		t.Fatal("a claim with the cockpit session on this host must read as the cockpit's")
	}
	// The heartbeat moves the refreshed stamp.
	first := res.Claim.Refreshed
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cur, _ := storage.ReadFinishClaim(projDir, "t-2"); cur != nil && cur.Refreshed != first {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cur, _ := storage.ReadFinishClaim(projDir, "t-2"); cur == nil || cur.Refreshed == first {
		t.Fatal("the heartbeat never refreshed the claim")
	}
	// Claiming again is a refresh, not a second claim.
	again, err := c.Claim("test", "t-2")
	if err != nil || !again.Refreshed {
		t.Fatalf("re-claim = %+v %v", again, err)
	}
	// Release: gone, idempotent.
	rel, err := c.Release("test", "t-2")
	if err != nil || !rel.Released {
		t.Fatalf("release = %+v %v", rel, err)
	}
	if cur, _ := storage.ReadFinishClaim(projDir, "t-2"); cur != nil {
		t.Fatal("the claim file must be gone")
	}
	if len(c.Held()) != 0 {
		t.Fatalf("held after release = %v", c.Held())
	}
	rel, err = c.Release("test", "t-2")
	if err != nil || rel.Released {
		t.Fatalf("second release = %+v %v", rel, err)
	}

	// Somebody else's claim: busy for both claim and release.
	if _, err := storage.AcquireFinishClaim(projDir, "t-2", "other-session"); err != nil {
		t.Fatal(err)
	}
	var busy *BusyError
	if _, err := c.Claim("test", "t-2"); !errors.As(err, &busy) || busy.Holder.Session != "other-session" {
		t.Fatalf("claim over a foreign claim = %v", err)
	}
	if _, err := c.Release("test", "t-2"); !errors.As(err, &busy) {
		t.Fatalf("release of a foreign claim = %v", err)
	}
	p, _ := c.Plan("test", "t-2", ActionClaim, Flags{})
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "other-session") {
		t.Fatalf("plan warnings = %v", p.Warnings)
	}
}
