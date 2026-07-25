package board

import (
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

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
