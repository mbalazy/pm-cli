package storage

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// crashedRunState writes a run-state that looks exactly like what a SIGKILLed
// manager leaves: status still "running", a pid that cannot be alive, and a last
// heartbeat some minutes after the start.
func crashedRunState(t *testing.T, dir, taskID, runID string) *RunState {
	t.Helper()
	started := time.Now().UTC().Add(-30 * time.Minute)
	st := &RunState{
		TaskID: taskID, RunID: runID, Project: "proj", Kind: RunKindEpic,
		Status: RunStatusRunning, PID: deadPID,
		Started: started.Format(time.RFC3339), Updated: started.Add(12 * time.Minute).Format(time.RFC3339),
		CurrentSub: taskID + "-2", Phase: "running",
	}
	// Written directly, NOT through WriteRunState: that re-stamps Updated to now,
	// and the whole point here is a last heartbeat that stopped in the past - which
	// is what a dead manager's file actually looks like.
	if err := os.MkdirAll(executorRunDir(dir), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ExecutorRunPath(dir, taskID), data, 0644); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestReconcileCrashedRunsClosesAnOrphanedStart(t *testing.T) {
	dir := t.TempDir()
	st := crashedRunState(t, dir, "proj-1", "run-abc")
	if err := AppendJournal(dir, &JournalEntry{
		Event: JournalEventStart, Kind: RunKindEpic, Project: "proj", TaskID: "proj-1", RunID: "run-abc", PID: st.PID,
	}); err != nil {
		t.Fatal(err)
	}

	got := ReconcileCrashedRuns(dir)
	if len(got) != 1 {
		t.Fatalf("reconciled %d run(s), want 1", len(got))
	}
	e := got[0]
	if e.Event != JournalEventCrashed || e.RunID != "run-abc" || e.TaskID != "proj-1" {
		t.Fatalf("wrong crash entry: %+v", e)
	}
	// The whole point of the line: it carries a reason, and the reason contains
	// what could actually be established.
	for _, want := range []string{"no signal recorded", "SIGKILL", st.Updated, "proj-1-2", "phase running"} {
		if !strings.Contains(e.Error, want) {
			t.Errorf("crash reason missing %q:\n%s", want, e.Error)
		}
	}
	if e.DurationS != 12*60 {
		t.Errorf("DurationS = %d, want the 720 s the run was demonstrably alive", e.DurationS)
	}

	// It is on disk, closing the start line.
	entries, err := ReadJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].Event != JournalEventCrashed {
		t.Fatalf("journal does not end with the crash line: %+v", entries)
	}
}

// TestReconcileCrashedRunsIsIdempotent: it runs at the start of EVERY executor
// run, so a crash must be recorded exactly once. The crashed line is itself a
// terminal line, which is what makes the second call see nothing to do.
func TestReconcileCrashedRunsIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	st := crashedRunState(t, dir, "proj-1", "run-abc")
	if err := AppendJournal(dir, &JournalEntry{
		Event: JournalEventStart, Kind: RunKindEpic, Project: "proj", TaskID: "proj-1", RunID: "run-abc", PID: st.PID,
	}); err != nil {
		t.Fatal(err)
	}

	if got := ReconcileCrashedRuns(dir); len(got) != 1 {
		t.Fatalf("first pass reconciled %d, want 1", len(got))
	}
	if got := ReconcileCrashedRuns(dir); len(got) != 0 {
		t.Fatalf("second pass reconciled %d, want 0 (the crash is already recorded)", len(got))
	}
}

func TestReconcileCrashedRunsLeavesClosedAndLiveRunsAlone(t *testing.T) {
	t.Run("a run that ended normally", func(t *testing.T) {
		dir := t.TempDir()
		st := crashedRunState(t, dir, "proj-1", "run-abc") // dead pid, but...
		for _, e := range []JournalEntry{
			{Event: JournalEventStart, Kind: RunKindEpic, Project: "proj", TaskID: "proj-1", RunID: "run-abc", PID: st.PID},
			{Event: JournalEventEnd, Kind: RunKindEpic, Project: "proj", TaskID: "proj-1", RunID: "run-abc", PID: st.PID, Status: RunStatusDone},
		} {
			entry := e
			if err := AppendJournal(dir, &entry); err != nil {
				t.Fatal(err)
			}
		}
		if got := ReconcileCrashedRuns(dir); len(got) != 0 {
			t.Fatalf("a run with an end line must not be reported as a crash: %+v", got)
		}
	})

	t.Run("a run whose manager is alive", func(t *testing.T) {
		dir := t.TempDir()
		live := &RunState{
			TaskID: "proj-1", RunID: "run-live", Project: "proj", Kind: RunKindEpic,
			Status: RunStatusRunning, PID: os.Getpid(),
			// Stamped NOW, not a minute ago: liveness is pid AND process start
			// time (a process older than the run's own stamp is a recycled pid),
			// and this test binary started seconds ago.
			Started: time.Now().UTC().Format(time.RFC3339),
			Updated: time.Now().UTC().Format(time.RFC3339),
		}
		if err := WriteRunState(dir, live); err != nil {
			t.Fatal(err)
		}
		if err := AppendJournal(dir, &JournalEntry{
			Event: JournalEventStart, Kind: RunKindEpic, Project: "proj", TaskID: "proj-1", RunID: "run-live", PID: live.PID,
		}); err != nil {
			t.Fatal(err)
		}
		if got := ReconcileCrashedRuns(dir); len(got) != 0 {
			t.Fatalf("a live run must not be reconciled: %+v", got)
		}
	})

	// A run-state written before run ids existed cannot be paired safely - a
	// recycled pid would let it close another run's start line - so it is skipped
	// rather than guessed at.
	t.Run("a run-state with no run id", func(t *testing.T) {
		dir := t.TempDir()
		st := crashedRunState(t, dir, "proj-1", "")
		if err := AppendJournal(dir, &JournalEntry{
			Event: JournalEventStart, Kind: RunKindEpic, Project: "proj", TaskID: "proj-1", PID: st.PID,
		}); err != nil {
			t.Fatal(err)
		}
		if got := ReconcileCrashedRuns(dir); len(got) != 0 {
			t.Fatalf("an id-less run must be left to the orphaned-start heuristic: %+v", got)
		}
	})
}
