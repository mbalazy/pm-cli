package storage

import (
	"os"
	"testing"
)

func TestRunStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := &RunState{
		TaskID:         "p-1",
		Project:        "p",
		Kind:           "run-epic",
		Status:         RunStatusRunning,
		PID:            os.Getpid(),
		CurrentSub:     "p-1-2",
		CurrentSession: "sess-abc",
		Subs: []SubRun{
			{ID: "p-1-1", Status: "merged"},
			{ID: "p-1-2", Status: RunStatusRunning, Session: "sess-abc"},
		},
	}
	if err := WriteRunState(dir, st); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}

	got, err := ReadRunState(dir, "p-1")
	if err != nil {
		t.Fatalf("ReadRunState: %v", err)
	}
	if got.Kind != "run-epic" || got.CurrentSub != "p-1-2" || got.CurrentSession != "sess-abc" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if len(got.Subs) != 2 || got.Subs[1].Session != "sess-abc" {
		t.Errorf("subs mismatch: %+v", got.Subs)
	}
	if got.Updated == "" {
		t.Error("WriteRunState should stamp Updated")
	}

	all := ReadRunStates(dir)
	if all["p-1"] == nil {
		t.Error("ReadRunStates should include p-1")
	}
}

func TestRunStateIsLive(t *testing.T) {
	// running + our own (live) pid -> live
	live := &RunState{Status: RunStatusRunning, PID: os.Getpid()}
	if !live.IsLive() {
		t.Error("running run with the current pid should be live")
	}
	// running but a pid that does not exist -> not live (stale)
	stale := &RunState{Status: RunStatusRunning, PID: 2147483640}
	if stale.IsLive() {
		t.Error("running run with a dead pid should not be live")
	}
	// done -> never live
	done := &RunState{Status: RunStatusDone, PID: os.Getpid()}
	if done.IsLive() {
		t.Error("a done run is never live")
	}
	if (&RunState{}).IsLive() {
		t.Error("zero-value run is not live")
	}
}

func TestReadRunStatesMissingDir(t *testing.T) {
	// No .executor dir -> empty map, no panic.
	if got := ReadRunStates(t.TempDir()); len(got) != 0 {
		t.Errorf("expected empty map for missing dir, got %v", got)
	}
}

func TestNewSessionIDUnique(t *testing.T) {
	a, b := NewSessionID(), NewSessionID()
	if a == b || a == "" {
		t.Errorf("session ids should be unique and non-empty: %q %q", a, b)
	}
	if len(a) != 36 { // 8-4-4-4-12
		t.Errorf("session id %q is not uuid-shaped", a)
	}
}
