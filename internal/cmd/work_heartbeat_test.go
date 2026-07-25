package cmd

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// The observable AC: while a worker is in flight, an outside observer polling
// the run-state must see the stamp advance - that is what separates a long sub
// from a hung run. Exercised through the real executeWork plumbing with a fake
// `claude` that sleeps long enough to cross a second boundary (RFC3339 stamps
// have second granularity).
func TestExecuteWorkHeartbeatsWhileWorkerRuns(t *testing.T) {
	fakeClaude(t, "sleep 2\necho '"+envelope("merged", "done")+"'")
	store, task, plan, opts := executorFixture(t)

	prev := workerHeartbeatInterval
	workerHeartbeatInterval = 20 * time.Millisecond
	t.Cleanup(func() { workerHeartbeatInterval = prev })

	done := make(chan error, 1)
	go func() {
		_, err := executeWork(store, task, plan, opts)
		done <- err
	}()

	dir := store.ProjectDir("app")
	var first *storage.RunState
	deadline := time.Now().Add(10 * time.Second)
	beat := false
	for !beat {
		if time.Now().After(deadline) {
			t.Fatal("run-state never advanced while the worker was running")
		}
		select {
		case err := <-done:
			t.Fatalf("worker finished before any heartbeat was observed (err=%v)", err)
		default:
		}
		got, err := storage.ReadRunState(dir, "app-1")
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if first == nil {
			first = got
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if got.Updated != first.Updated {
			// Mid-worker: the run is still running and only the stamp moved.
			if got.Status != storage.RunStatusRunning {
				t.Fatalf("stamp advanced only after the run ended (status %s)", got.Status)
			}
			beat = true
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}

	if err := <-done; err != nil {
		t.Fatalf("executeWork: %v", err)
	}
	final, err := storage.ReadRunState(dir, "app-1")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	if final.Status != storage.RunStatusDone {
		t.Fatalf("run should be done, got %s", final.Status)
	}
}

// After the worker returns nothing may stamp the file again - the heartbeat
// dies with the worker, so a finished run's Updated stays the end stamp (which
// is what the board renders as the run's elapsed time).
func TestExecuteWorkHeartbeatStopsWithTheWorker(t *testing.T) {
	fakeClaude(t, "echo '"+envelope("merged", "done")+"'")
	store, task, plan, opts := executorFixture(t)

	prev := workerHeartbeatInterval
	workerHeartbeatInterval = 5 * time.Millisecond
	t.Cleanup(func() { workerHeartbeatInterval = prev })

	if _, err := executeWork(store, task, plan, opts); err != nil {
		t.Fatalf("executeWork: %v", err)
	}
	// Compare file mtimes, NOT the Updated field: Updated has second
	// granularity, so a leaking heartbeat would slip past a stamp comparison
	// unless a second boundary happened to fall inside the window. Every write
	// is a tmp+rename, so mtime moves on every beat.
	path := storage.ExecutorRunPath(store.ProjectDir("app"), "app-1")
	settled, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // many missed beats at a 5ms interval
	later, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !later.ModTime().Equal(settled.ModTime()) {
		t.Errorf("run-state rewritten after the run ended (%v -> %v)", settled.ModTime(), later.ModTime())
	}
}

// The epic half of the AC: an epic sub's worker heartbeats the MANAGER's shared
// epic-level run-state, handed down through workOptions.runWriter. Without this
// the standalone tests would stay green with the whole epic path deleted.
func TestExecuteWorkHeartbeatsEpicManagerRunState(t *testing.T) {
	fakeClaude(t, "sleep 2\necho '"+envelope("merged", "done")+"'")
	store, task, plan, opts := executorFixture(t)

	prevInterval := workerHeartbeatInterval
	workerHeartbeatInterval = 20 * time.Millisecond
	t.Cleanup(func() { workerHeartbeatInterval = prevInterval })

	// Epic mode: the manager owns the run-state, keyed by the TRACKER, and the
	// worker only borrows the writer.
	stateDir := store.ProjectDir("app")
	rw := storage.NewRunWriter(stateDir, &storage.RunState{
		TaskID: "app-tracker", Project: "app", Kind: "run-epic", Status: storage.RunStatusRunning,
		PID: os.Getpid(), Started: time.Now().UTC().Format(time.RFC3339),
		CurrentSub: task.Meta.ID,
		Subs:       []storage.SubRun{{ID: task.Meta.ID, Status: storage.RunStatusRunning}},
	})
	if err := rw.Update(nil); err != nil {
		t.Fatalf("seed run-state: %v", err)
	}
	opts.standalone = false
	opts.runWriter = rw

	path := storage.ExecutorRunPath(stateDir, "app-tracker")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := executeWork(store, task, plan, opts)
		done <- err
	}()

	beat := false
	deadline := time.Now().Add(10 * time.Second)
	for !beat {
		if time.Now().After(deadline) {
			t.Fatal("the epic-level run-state never got stamped while the sub's worker ran")
		}
		select {
		case err := <-done:
			t.Fatalf("worker finished before any heartbeat was observed (err=%v)", err)
		default:
		}
		now, err := os.Stat(path)
		if err == nil && !now.ModTime().Equal(before.ModTime()) {
			beat = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := <-done; err != nil {
		t.Fatalf("executeWork: %v", err)
	}
	// The worker must not have touched the epic run-state's own fields - only
	// the manager writes those.
	got, err := storage.ReadRunState(stateDir, "app-tracker")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	if got.Status != storage.RunStatusRunning || got.CurrentSub != task.Meta.ID {
		t.Errorf("worker mutated the manager's run-state: %+v", got)
	}
	// And no standalone run-state was created for the sub in epic mode.
	if _, err := storage.ReadRunState(stateDir, task.Meta.ID); !os.IsNotExist(err) {
		t.Errorf("epic mode must not write a per-sub run-state, got err=%v", err)
	}
	// The in-flight session marker (what the board gates the heartbeat age on)
	// must be dropped as soon as the worker returns, so the manager's
	// post-worker tail never renders a frozen stamp as a live beat.
	if got.CurrentSession != "" {
		t.Errorf("CurrentSession must be cleared when the worker returns, got %q", got.CurrentSession)
	}
}

// The manager side of the same AC: driveSubIndependent must hand its worker the
// epic-level writer. Covers the wiring itself - the executeWork tests above
// inject a writer by hand and so cannot catch a manager that never passes one.
func TestDriveSubIndependentHeartbeatsTheEpicRunState(t *testing.T) {
	// The fake worker commits (so pushIfAhead has something to see) and lives
	// long enough for several beats.
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\nsleep 2\necho '"+envelope("merged", "done")+"'")
	store, sub, _, opts := executorFixture(t)

	prevInterval := workerHeartbeatInterval
	workerHeartbeatInterval = 20 * time.Millisecond
	t.Cleanup(func() { workerHeartbeatInterval = prevInterval })

	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "app-t", Title: "Batch", Status: storage.StatusDoing}, Project: "app"}
	if err := store.AddTask("app", tracker); err != nil {
		t.Fatal(err)
	}
	tracker, _ = store.FindTask("app", "app-t")

	proj, _ := store.GetProject("app")
	stateDir := store.ProjectDir("app")
	rw := storage.NewRunWriter(stateDir, &storage.RunState{
		TaskID: "app-t", Project: "app", Kind: "run-epic", Status: storage.RunStatusRunning,
		PID: os.Getpid(), Started: time.Now().UTC().Format(time.RFC3339),
		Subs: []storage.SubRun{{ID: sub.Meta.ID, Status: storage.RunStatusRunning}},
	})
	if err := rw.Update(nil); err != nil {
		t.Fatal(err)
	}
	opts.standalone = false
	opts.independent = true

	stopWatch := watchRunStateWrites(storage.ExecutorRunPath(stateDir, "app-t"))
	oc := driveSubIndependent(store, proj.Path, "app", tracker, sub, gitHeadBranch(t, proj.Path), storage.StatusDone, opts, rw, false)
	writes := stopWatch()

	if oc.result != "merged" {
		t.Fatalf("sub outcome = %+v", oc)
	}
	if writes < 20 {
		t.Errorf("epic run-state only saw %d distinct writes - the manager is not passing its writer to the worker (the driver alone writes ~3)", writes)
	}
}

// Same guard for integration mode: driveSub must hand its worker the writer too.
func TestDriveSubHeartbeatsTheEpicRunState(t *testing.T) {
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\nsleep 2\necho '"+envelope("merged", "done")+"'")
	store, sub, _, opts := executorFixture(t)

	prevInterval := workerHeartbeatInterval
	workerHeartbeatInterval = 20 * time.Millisecond
	t.Cleanup(func() { workerHeartbeatInterval = prevInterval })

	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "app-t", Title: "Epic", Status: storage.StatusDoing}, Project: "app"}
	if err := store.AddTask("app", tracker); err != nil {
		t.Fatal(err)
	}
	tracker, _ = store.FindTask("app", "app-t")

	proj, _ := store.GetProject("app")
	gitT(t, proj.Path, "checkout", "-q", "-B", "epic/app-t")

	stateDir := store.ProjectDir("app")
	rw := storage.NewRunWriter(stateDir, &storage.RunState{
		TaskID: "app-t", Project: "app", Kind: "run-epic", Status: storage.RunStatusRunning,
		PID: os.Getpid(), Started: time.Now().UTC().Format(time.RFC3339),
		Subs: []storage.SubRun{{ID: sub.Meta.ID, Status: storage.RunStatusRunning}},
	})
	if err := rw.Update(nil); err != nil {
		t.Fatal(err)
	}
	opts.standalone = false

	stopWatch := watchRunStateWrites(storage.ExecutorRunPath(stateDir, "app-t"))
	oc := driveSub(store, proj.Path, "app", tracker, sub, "epic/app-t", storage.StatusDone, opts, rw, false)
	writes := stopWatch()

	if oc.result != "merged" {
		t.Fatalf("sub outcome = %+v", oc)
	}
	if writes < 20 {
		t.Errorf("epic run-state only saw %d distinct writes - the manager is not passing its writer to the worker (the driver alone writes ~3)", writes)
	}
}

// watchRunStateWrites polls path and counts DISTINCT mtimes until the returned
// stop func is called, which returns the count. Distinct mtimes = number of
// tmp+rename writes observed, which is how a heartbeat proves itself without
// depending on the second-granularity Updated field.
func watchRunStateWrites(path string) func() int {
	done := make(chan struct{})
	out := make(chan int, 1)
	go func() {
		seen := map[int64]bool{}
		for {
			if fi, err := os.Stat(path); err == nil {
				seen[fi.ModTime().UnixNano()] = true
			}
			select {
			case <-done:
				out <- len(seen)
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	var once sync.Once
	return func() int {
		once.Do(func() { close(done) })
		return <-out
	}
}

// gitHeadBranch is the repo's current branch (the fixture repo's default init
// branch name is a git config detail, not something to hardcode).
func gitHeadBranch(t *testing.T, dir string) string {
	t.Helper()
	b, err := gitCurrentBranch(dir)
	if err != nil || b == "" {
		t.Fatalf("resolve HEAD branch in %s: %v", dir, err)
	}
	return b
}
