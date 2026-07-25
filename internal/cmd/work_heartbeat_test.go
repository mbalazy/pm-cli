package cmd

import (
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
	settled, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // many missed beats
	later, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	if later.Updated != settled.Updated {
		t.Errorf("run-state stamped after the run ended: %q -> %q", settled.Updated, later.Updated)
	}
}
