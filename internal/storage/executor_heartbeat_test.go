package storage

import (
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

// newHBWriter: a writer over a running run-state already on disk.
func newHBWriter(t *testing.T) (*RunWriter, string) {
	t.Helper()
	dir := t.TempDir()
	w := NewRunWriter(dir, &RunState{
		TaskID: "p-1", Project: "p", Kind: "run-epic", Status: RunStatusRunning, PID: os.Getpid(),
		Started: time.Now().UTC().Format(time.RFC3339),
		Subs:    []SubRun{{ID: "p-1-1", Status: RunStatusRunning}},
	})
	if err := w.Update(nil); err != nil {
		t.Fatalf("initial Update: %v", err)
	}
	return w, dir
}

func TestRunWriterHeartbeatAdvancesUpdated(t *testing.T) {
	w, dir := newHBWriter(t)
	first, err := ReadRunState(dir, "p-1")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}

	stop := w.Heartbeat(20 * time.Millisecond)
	defer stop()

	// Updated is RFC3339 (second granularity), so the stamp only flips at the
	// next second boundary - poll instead of sleeping a fixed amount.
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := ReadRunState(dir, "p-1")
		if err == nil && got.Updated != first.Updated {
			// The heartbeat stamps the time and NOTHING else - it must never
			// guess a phase, invent subs or touch a note. Compare the WHOLE
			// struct with the stamp normalised away.
			normalised := *got
			normalised.Updated = first.Updated
			if !reflect.DeepEqual(&normalised, first) {
				t.Fatalf("heartbeat changed state beyond the stamp:\n got %+v\nwant %+v", got, first)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Updated never advanced (still %q) - the heartbeat is not stamping", first.Updated)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunWriterHeartbeatFreezesOnStop(t *testing.T) {
	w, dir := newHBWriter(t)
	stop := w.Heartbeat(5 * time.Millisecond)
	time.Sleep(30 * time.Millisecond) // let it beat a few times
	stop()

	// Once stop() returns the goroutine is gone, so the file must be frozen - a
	// heartbeat may never outlive the worker it was started for.
	after, err := os.Stat(ExecutorRunPath(dir, "p-1"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // several missed beats
	later, err := os.Stat(ExecutorRunPath(dir, "p-1"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !later.ModTime().Equal(after.ModTime()) {
		t.Errorf("run-state was rewritten after stop (%v -> %v)", after.ModTime(), later.ModTime())
	}

	stop() // idempotent: a second stop must not panic or block
}

func TestRunWriterConcurrentUpdatesAreSerialized(t *testing.T) {
	// The point of RunWriter: the heartbeat goroutine and the manager mutate the
	// SAME struct. `make check` does NOT pass -race, so the writers here are
	// deliberately CONCURRENT and increment a counter: without the mutex the
	// read-modify-write interleaves and updates are lost, which fails this test
	// on the plain suite too (under -race it also reports the race directly).
	w, dir := newHBWriter(t)
	stop := w.Heartbeat(time.Millisecond)

	const writers, each = 4, 50
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				_ = w.Update(func(st *RunState) {
					st.CurrentSub = "p-1-1"
					st.CurrentSession = "sess"
					st.Phase = "running"
					// Read-modify-write with a forced reschedule in the middle.
					// Under the lock this is still exact; without it the window
					// is wide enough that the writers lose updates on every run
					// rather than roughly half of them (a bare `st.Turns++`
					// only detects a deleted mutex ~50% of the time, and this
					// test is the ONLY guard on the plain suite - `make check`
					// does not pass -race).
					n := st.Subs[0].Turns
					time.Sleep(10 * time.Microsecond)
					st.Subs[0].Turns = n + 1
				})
			}
		}()
	}
	wg.Wait()
	stop()

	got, err := ReadRunState(dir, "p-1")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	if got.Subs[0].Turns != writers*each {
		t.Errorf("lost updates: Turns = %d, want %d", got.Subs[0].Turns, writers*each)
	}
	var turns int
	w.Read(func(st *RunState) { turns = st.Subs[0].Turns })
	if turns != writers*each {
		t.Errorf("Read saw %d turns, want %d", turns, writers*each)
	}
}

func TestRunWriterNilIsNoOp(t *testing.T) {
	var w *RunWriter
	if err := w.Update(func(*RunState) { t.Error("fn must not run on a nil writer") }); err != nil {
		t.Errorf("nil Update: %v", err)
	}
	w.Read(func(*RunState) { t.Error("fn must not run on a nil writer") })
	w.Heartbeat(time.Millisecond)() // no-op stop, must not panic or block
}

func TestRunWriterHeartbeatNonPositiveInterval(t *testing.T) {
	w, dir := newHBWriter(t)
	before, err := os.Stat(ExecutorRunPath(dir, "p-1"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	stop := w.Heartbeat(0)
	time.Sleep(20 * time.Millisecond)
	stop()
	after, err := os.Stat(ExecutorRunPath(dir, "p-1"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("a non-positive interval must disable the heartbeat entirely")
	}
}
