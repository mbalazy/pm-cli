package board

import (
	"errors"
	"os/exec"
	"strings"
	"syscall"
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

// liveRunPID starts a detached sleeper and returns its pid, so a run-state
// fixture can name a process that genuinely exists. Since 0.37.1 a kill refuses
// to signal a pid it cannot prove belongs to the run, so a fixture pid that was
// never alive no longer exercises the "stopped it" path at all - it exercises
// the refusal. The sleeper leads its own group (like a detached manager) and is
// reaped so a terminated child does not linger as a zombie.
func liveRunPID(t *testing.T) int {
	t.Helper()
	c := exec.Command("sleep", "30")
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = c.Process.Wait() }()
	t.Cleanup(func() { _ = c.Process.Kill() })
	return c.Process.Pid
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
		PID: liveRunPID(t), Started: time.Now().UTC().Format(time.RFC3339), CurrentSub: "p-2",
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
		PID: liveRunPID(t), Started: time.Now().UTC().Format(time.RFC3339), CurrentSub: "p-2",
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

// A run-state outlives its process: a kill -9, an OOM or a reboot leaves
// `status: running` behind with a pid nobody cleaned up, and after a reboot that
// number gets handed to something else. Pressing K on that card must not fire a
// SIGTERM (and two seconds later a SIGKILL) at a stranger's process GROUP - and
// the user must be told the run was already gone rather than that it was
// stopped, with no escalation scheduled against the pid pm just refused.
func TestKillRunOnAStaleRunStateSignalsNothing(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-2", Title: "in flight", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")

	// pid 1 is alive on every machine and demonstrably did NOT start in 2019:
	// the recycled-number shape, without a test ever naming a real victim.
	run := &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: "run-epic", Status: storage.RunStatusRunning,
		PID: 1, Started: "2019-01-01T00:00:00Z", CurrentSub: "p-2",
		Subs: []storage.SubRun{{ID: "p-2", Status: storage.RunStatusRunning}},
	}
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}

	if cmd := m.killRun(run); cmd != nil {
		t.Error("nothing was signalled, so no SIGKILL escalation may be scheduled")
	}
	if !strings.Contains(m.toastMsg, "already gone") || !strings.Contains(m.toastMsg, "nothing signalled") {
		t.Errorf("toastMsg = %q, want it to say the run was already gone and nothing was signalled", m.toastMsg)
	}

	// The state is still reconciled with reality - that is the honest outcome
	// of pressing K on a run that is not there.
	got, err := storage.ReadRunState(stateDir, "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != storage.RunStatusFailed {
		t.Errorf("run status = %q, want failed", got.Status)
	}
	if got.Subs[0].Status != "blocked" {
		t.Errorf("in-flight sub status = %q, want blocked", got.Subs[0].Status)
	}
	parked, err := m.store.FindTask("p", "p-2")
	if err != nil {
		t.Fatal(err)
	}
	if parked.Meta.Status != storage.StatusWaiting {
		t.Errorf("parked task status = %q, want waiting", parked.Meta.Status)
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
