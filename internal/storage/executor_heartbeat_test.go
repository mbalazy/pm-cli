package storage

import (
	"os"
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
			// guess a phase or invent subs.
			if got.Status != first.Status || got.CurrentSub != first.CurrentSub || len(got.Subs) != len(first.Subs) {
				t.Fatalf("heartbeat changed state beyond the stamp: %+v vs %+v", got, first)
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
	// SAME struct. Run under -race to catch the field-level race that atomic
	// file writes alone would not prevent.
	w, dir := newHBWriter(t)
	stop := w.Heartbeat(time.Millisecond)

	for i := 0; i < 200; i++ {
		_ = w.Update(func(st *RunState) {
			st.CurrentSub = "p-1-1"
			st.CurrentSession = "sess"
			st.Phase = "running"
			st.Subs[0].Turns++
		})
	}
	stop()

	got, err := ReadRunState(dir, "p-1")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	if got.Subs[0].Turns != 200 {
		t.Errorf("lost updates: Turns = %d, want 200", got.Subs[0].Turns)
	}
	var turns int
	w.Read(func(st *RunState) { turns = st.Subs[0].Turns })
	if turns != 200 {
		t.Errorf("Read saw %d turns, want 200", turns)
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
