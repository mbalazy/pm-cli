package board

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// moveTaskFailStore wraps a real TaskStore and forces MoveTask to fail, so
// tests can exercise the "MoveTask legally failed" path (e.g. the task file
// was deleted mid-run) without racing a real deletion.
type moveTaskFailStore struct {
	storage.TaskStore
	err error
}

func (s *moveTaskFailStore) MoveTask(t *storage.Task, newStatus storage.TaskStatus) error {
	return s.err
}

// TestKillRunJournalsSubsAndDuration covers Bug 2 (pm-cli-37): killRun's
// journaled "killed" line must carry the per-sub outcomes it just read off
// the run-state (so a killed epic still contributes to pm executor stats'
// sub histogram/turns/cost) and a DurationS computed from RunState.Started.
func TestKillRunJournalsSubsAndDuration(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-2", Title: "in flight", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")

	started := time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339)
	run := &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: "run-epic", Status: storage.RunStatusRunning,
		PID: 999999999, Started: started, CurrentSub: "p-2",
		Subs: []storage.SubRun{
			{ID: "p-1", Status: "merged", Note: "done", Turns: 12, CostUSD: 0.4},
			{ID: "p-2", Status: "running"},
		},
	}
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}

	m.killRun(run)

	entries, err := storage.ReadJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Event != storage.JournalEventKilled {
		t.Fatalf("expected one killed journal entry, got %+v", entries)
	}
	e := entries[0]

	if e.DurationS < 60 || e.DurationS > 150 {
		t.Errorf("DurationS = %d, want ~90 (computed from RunState.Started)", e.DurationS)
	}
	if len(e.Subs) != 2 {
		t.Fatalf("expected both subs carried into the killed line, got %+v", e.Subs)
	}
	// p-1 was already merged before the kill - it must still land in the
	// journal so it counts toward the stats histogram/turns/cost instead of
	// the whole killed run contributing nothing despite real token spend.
	if e.Subs[0].ID != "p-1" || e.Subs[0].Result != "merged" || e.Subs[0].Turns != 12 || e.Subs[0].CostUSD != 0.4 {
		t.Errorf("merged sub not carried correctly: %+v", e.Subs[0])
	}
	// p-2 was in flight when killed - killRun parks it as "blocked" just
	// above the journal write, and that update must be what's journaled.
	if e.Subs[1].ID != "p-2" || e.Subs[1].Result != "blocked" {
		t.Errorf("in-flight sub not carried as blocked: %+v", e.Subs[1])
	}
}

// TestKillRunParkFailureShowsErrorToast covers the out-of-AC finding from the
// 67-3 odbiór: MoveTask can legally fail (e.g. the task file was deleted
// while the run was live) since 67-3's resurrection-hole fix - the toast must
// not unconditionally claim the in-flight task was parked on waiting.
func TestKillRunParkFailureShowsErrorToast(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-2", Title: "in flight", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	failErr := errors.New("task file gone")
	m.store = &moveTaskFailStore{TaskStore: m.store, err: failErr}

	run := &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: "run-epic", Status: storage.RunStatusRunning,
		PID: 999999999, Started: time.Now().UTC().Format(time.RFC3339), CurrentSub: "p-2",
	}
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}

	m.killRun(run)

	if m.toastMsg == "Stopped executor run p-9 (parked p-2 on waiting)" {
		t.Fatalf("toast claims the park succeeded despite MoveTask returning an error: %q", m.toastMsg)
	}
	if !strings.Contains(m.toastMsg, "failed to park") || !strings.Contains(m.toastMsg, failErr.Error()) {
		t.Errorf("toastMsg = %q, want it to mention the park failure and %q", m.toastMsg, failErr.Error())
	}
}

// TestKillRunParkSuccessShowsWaitingToast is the counterpart: when MoveTask
// succeeds, the toast keeps its original wording.
func TestKillRunParkSuccessShowsWaitingToast(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-2", Title: "in flight", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")

	run := &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: "run-epic", Status: storage.RunStatusRunning,
		PID: 999999999, Started: time.Now().UTC().Format(time.RFC3339), CurrentSub: "p-2",
	}
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}

	m.killRun(run)

	want := "Stopped executor run p-9 (parked p-2 on waiting)"
	if m.toastMsg != want {
		t.Errorf("toastMsg = %q, want %q", m.toastMsg, want)
	}
}

// TestKillRunJournalsZeroDurationOnBadStarted covers the "never guess"
// contract: an unparseable/empty RunState.Started must leave DurationS at its
// zero value rather than a fabricated number.
func TestKillRunJournalsZeroDurationOnBadStarted(t *testing.T) {
	m := newBoardModel(t)
	stateDir := m.store.ProjectDir("p")

	run := &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: "work", Status: storage.RunStatusRunning,
		PID: 999999999, Started: "",
	}
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}

	m.killRun(run)

	entries, err := storage.ReadJournal(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one killed journal entry, got %+v", entries)
	}
	if entries[0].DurationS != 0 {
		t.Errorf("DurationS = %d, want 0 for an unparseable Started stamp", entries[0].DurationS)
	}
}
