package board

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/mbalazy/pm/internal/storage"
)

// The board's side of "the acceptance is a run like any other": a run and the
// acceptance OF that run share a task id and are routinely live at the same
// time (batch-finish-auto starts before the batch ends), so neither may evict,
// overwrite or stand in for the other anywhere on screen.

// liveRun/liveFinish build the two run-states of one tracker, both alive.
func liveRun(t *testing.T, taskID string, pid int) *storage.RunState {
	t.Helper()
	return &storage.RunState{
		TaskID: taskID, Project: "p", Kind: storage.RunKindEpic, Status: storage.RunStatusRunning,
		PID: pid, Started: time.Now().UTC().Format(time.RFC3339), CurrentSub: taskID + "-1",
		Subs: []storage.SubRun{{ID: taskID + "-1", Status: storage.RunStatusRunning, Session: "sess-run"}},
	}
}

func liveFinish(t *testing.T, taskID string, pid int) *storage.RunState {
	t.Helper()
	return &storage.RunState{
		TaskID: taskID, Project: "p", Kind: storage.RunKindFinish, Status: storage.RunStatusRunning,
		PID: pid, Started: time.Now().UTC().Format(time.RFC3339), CurrentSub: taskID,
		Subs: []storage.SubRun{{ID: taskID, Status: storage.RunStatusRunning, Session: "sess-finish"}},
	}
}

// Both states live at once: two maps, two badges, two dashboards, nothing
// displacing anything.
func TestRunAndAcceptanceAreBothVisible(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	pid := liveRunPID(t)
	for _, st := range []*storage.RunState{liveRun(t, "p-9", pid), liveFinish(t, "p-9", pid)} {
		if err := storage.WriteRunState(stateDir, st); err != nil {
			t.Fatal(err)
		}
	}
	m.refreshRunStates()

	if got := m.runStates["p-9"]; got == nil || got.Kind != storage.RunKindEpic {
		t.Fatalf("the run was displaced by its acceptance: %+v", got)
	}
	if got := m.finishStates["p-9"]; got == nil || got.Kind != storage.RunKindFinish {
		t.Fatalf("the acceptance was displaced by the run: %+v", got)
	}
	if rb, fb := m.runBadge("p-9"), m.finishBadge("p-9"); rb != "▶ running" || fb != "▶ accepting" {
		t.Errorf("badges = %q / %q, want both live and distinguishable", rb, fb)
	}

	// The card carries both at once - that combined state is the one worth
	// seeing, so one badge must not replace the other.
	board := m.View()
	if !strings.Contains(board, "running") || !strings.Contains(board, "accepting") {
		t.Errorf("the card must badge both runs:\n%s", board)
	}

	// So does the detail view: two dashboards, each with its own status and
	// clock, never merged into one.
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}
	detail := m.renderTaskDetail(task)
	if !strings.Contains(detail, "Executor run") {
		t.Errorf("detail is missing the run dashboard:\n%s", detail)
	}
	if !strings.Contains(detail, "Acceptance run") {
		t.Errorf("detail is missing the acceptance dashboard:\n%s", detail)
	}
}

// K on an acceptance stops THAT run: its own run-state is stamped failed, its
// claim is released on the killed process's behalf, and the run being accepted
// is left completely alone - including the tracker's status, which an
// acceptance never had any business moving.
func TestKillRunOnAnAcceptanceReleasesTheClaimAndSparesTheRun(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	pid := liveRunPID(t)
	run := liveRun(t, "p-9", pid)
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}
	fin := liveFinish(t, "p-9", pid)
	if err := storage.WriteRunState(stateDir, fin); err != nil {
		t.Fatal(err)
	}
	// The claim the killed process holds: same host, same pid.
	claim, err := storage.AcquireFinishClaim(stateDir, "p-9", "sess-finish")
	if err != nil {
		t.Fatal(err)
	}
	claim.PID = pid
	writeClaimFixture(t, stateDir, claim)

	cmd := m.killRun(fin)

	got, err := storage.ReadFinishRunState(stateDir, "p-9")
	if err != nil {
		t.Fatalf("acceptance run-state: %v", err)
	}
	if got.Status != storage.RunStatusFailed || got.Kind != storage.RunKindFinish {
		t.Errorf("the acceptance must be stamped failed in its OWN file: %+v", got)
	}
	kept, err := storage.ReadRunState(stateDir, "p-9")
	if err != nil || kept.Kind != storage.RunKindEpic || kept.Status != storage.RunStatusRunning {
		t.Fatalf("killing the acceptance touched the run it accepts: %+v err=%v", kept, err)
	}
	if held, _ := storage.ReadFinishClaim(stateDir, "p-9"); held != nil {
		t.Errorf("the claim must be released for the killed process: %+v", held)
	}
	// The acceptance's one "sub" IS the tracker, and pm never moves a tracker.
	tracker, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if tracker.Meta.Status != storage.StatusDoing {
		t.Errorf("tracker status = %q, want it untouched (an acceptance parks nothing)", tracker.Meta.Status)
	}
	if !strings.Contains(m.toastMsg, "acceptance run") || !strings.Contains(m.toastMsg, "claim released") {
		t.Errorf("toastMsg = %q, want it to name what was stopped and what was released", m.toastMsg)
	}
	// The SIGKILL escalation must carry the kind, or it would re-read the RUN's
	// state two seconds later and signal a healthy manager's pid.
	if cmd == nil {
		t.Fatal("a signalled kill must schedule its escalation")
	}
	msg, ok := cmd().(execKillCheckMsg)
	if !ok || msg.kind != storage.RunKindFinish || msg.pid != pid {
		t.Errorf("escalation msg = %+v (ok=%v), want the acceptance's kind and pid", msg, ok)
	}
}

// A run-state outlives its process, and after a reboot its pid belongs to
// somebody else. The acceptance is no different from the run here: nothing is
// signalled, nothing is escalated, and the user is told it was already gone -
// but the state IS reconciled and the stale claim goes with it, which is what
// stops the tracker being locked out for the rest of the TTL.
func TestKillRunOnAStaleAcceptanceSignalsNothing(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")

	// pid 1 is alive on every machine and demonstrably did not start in 2019.
	fin := liveFinish(t, "p-9", 1)
	fin.Started = "2019-01-01T00:00:00Z"
	if err := storage.WriteRunState(stateDir, fin); err != nil {
		t.Fatal(err)
	}
	claim, err := storage.AcquireFinishClaim(stateDir, "p-9", "sess-finish")
	if err != nil {
		t.Fatal(err)
	}
	claim.PID = 1
	writeClaimFixture(t, stateDir, claim)

	if cmd := m.killRun(fin); cmd != nil {
		t.Error("nothing was signalled, so no SIGKILL escalation may be scheduled")
	}
	if !strings.Contains(m.toastMsg, "already gone") || !strings.Contains(m.toastMsg, "nothing signalled") {
		t.Errorf("toastMsg = %q, want it to say the run was already gone", m.toastMsg)
	}
	got, err := storage.ReadFinishRunState(stateDir, "p-9")
	if err != nil || got.Status != storage.RunStatusFailed {
		t.Errorf("the acceptance state must still be reconciled: %+v err=%v", got, err)
	}
	if held, _ := storage.ReadFinishClaim(stateDir, "p-9"); held != nil {
		t.Errorf("a claim naming the same dead pid must go too: %+v", held)
	}
}

// A claim that is NOT the killed run's - another machine, or another pid on
// this one - is left exactly where it is. killRun frees the lock its own
// process held, never whatever lock it happens to find.
func TestKillRunLeavesSomebodyElsesClaimAlone(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	fin := liveFinish(t, "p-9", liveRunPID(t))
	if err := storage.WriteRunState(stateDir, fin); err != nil {
		t.Fatal(err)
	}
	claim, err := storage.AcquireFinishClaim(stateDir, "p-9", "somebody-else")
	if err != nil {
		t.Fatal(err)
	}
	claim.PID = fin.PID + 1 // a different acceptance, same machine
	writeClaimFixture(t, stateDir, claim)

	m.killRun(fin)

	held, err := storage.ReadFinishClaim(stateDir, "p-9")
	if err != nil || held == nil || held.Session != "somebody-else" {
		t.Errorf("a claim this run never took must survive the kill: %+v err=%v", held, err)
	}
}

// K on a card prefers the RUN while both are live (it is the one spending a
// worker on code) and falls through to the acceptance when the run is not.
func TestKillTargetPrefersTheLiveRun(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	pid := liveRunPID(t)
	run, fin := liveRun(t, "p-9", pid), liveFinish(t, "p-9", pid)
	for _, st := range []*storage.RunState{run, fin} {
		if err := storage.WriteRunState(stateDir, st); err != nil {
			t.Fatal(err)
		}
	}
	m.refreshRunStates()
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.killTargetForTask(task); got == nil || got.Kind != storage.RunKindEpic {
		t.Errorf("with both live, K must target the run: %+v", got)
	}

	// Run finished, acceptance still going: K now means the acceptance.
	run.Status = storage.RunStatusDone
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	if got := m.killTargetForTask(task); got == nil || got.Kind != storage.RunKindFinish {
		t.Errorf("with only the acceptance live, K must target it: %+v", got)
	}
}

// W opens the RUN by default even with an acceptance live, and W inside the
// agent-view switches between the two - no new board key, and each side keeps
// its own transcript list.
func TestAgentViewSwitchesBetweenRunAndAcceptance(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	pid := liveRunPID(t)
	for _, st := range []*storage.RunState{liveRun(t, "p-9", pid), liveFinish(t, "p-9", pid)} {
		if err := storage.WriteRunState(stateDir, st); err != nil {
			t.Fatal(err)
		}
	}
	m.refreshRunStates()
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}

	if !m.openExecutorView(task) {
		t.Fatal("openExecutorView returned false with two run-states on disk")
	}
	if m.watchingFinish() {
		t.Error("the agent-view must open on the RUN when there is one")
	}
	if !m.executorHasOther {
		t.Error("the acceptance exists, so the W toggle must advertise itself")
	}
	if !strings.Contains(m.viewExecutor(), "W the acceptance run") {
		t.Errorf("the footer must offer the switch:\n%s", m.viewExecutor())
	}

	m.switchExecutorRunKind()
	if !m.watchingFinish() {
		t.Fatal("W did not switch to the acceptance")
	}
	if m.executorRun == nil || m.executorRun.Kind != storage.RunKindFinish {
		t.Errorf("the view is still reading the run's file: %+v", m.executorRun)
	}
	if len(m.executorSessions) != 1 || m.executorSessions[0].session != "sess-finish" {
		t.Errorf("the transcript list must be the acceptance's: %+v", m.executorSessions)
	}
	if !strings.Contains(m.viewExecutor(), "Acceptance ·") {
		t.Errorf("the header must name what is on screen:\n%s", m.viewExecutor())
	}

	m.switchExecutorRunKind()
	if m.watchingFinish() || m.executorRun.Kind != storage.RunKindEpic {
		t.Errorf("W must switch back to the run: %+v", m.executorRun)
	}
}

// With only one of the two on disk there is nothing to switch to: W says so
// instead of blanking the view.
func TestAgentViewSwitchRefusesWithoutACounterpart(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	fin := liveFinish(t, "p-9", liveRunPID(t))
	if err := storage.WriteRunState(stateDir, fin); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}

	// No run at all: W on the board opens the acceptance rather than reporting
	// nothing to watch.
	if !m.openExecutorView(task) {
		t.Fatal("openExecutorView must fall back to the acceptance")
	}
	if !m.watchingFinish() {
		t.Fatalf("expected the acceptance to be on screen, kind = %q", m.executorRunKind)
	}
	if m.executorHasOther {
		t.Error("there is no run to switch to")
	}

	before := m.executorRunKind
	m.switchExecutorRunKind()
	if m.executorRunKind != before {
		t.Errorf("the view switched to a run that does not exist: %q", m.executorRunKind)
	}
	if !strings.Contains(m.toastMsg, "no executor run recorded") {
		t.Errorf("toastMsg = %q, want it to say there is nothing to switch to", m.toastMsg)
	}
}

// The kill-confirm footer must describe what K will actually do: an acceptance
// parks no task, so promising a park there would be the prompt describing a
// different action.
func TestAcceptanceKillConfirmDoesNotPromiseAPark(t *testing.T) {
	m := newBoardModel(t)
	m.executorViewport = viewport.New(80, 10)
	m.executorRunTaskID = "p-9"
	m.executorRunKind = storage.RunKindFinish
	m.confirmAction = "kill-run"
	if got := m.viewExecutor(); !strings.Contains(got, "releases the acceptance claim") || strings.Contains(got, "parks the worker") {
		t.Errorf("confirm footer must describe the acceptance's own effect:\n%s", got)
	}
	m.executorRunKind = storage.RunKindEpic
	if got := m.viewExecutor(); !strings.Contains(got, "parks the worker") {
		t.Errorf("a run's confirm footer is unchanged:\n%s", got)
	}
}

// writeClaimFixture rewrites a claim file in place, so a test can name a holder
// (a pid, another session) that AcquireFinishClaim would only ever stamp with
// this process's own.
func writeClaimFixture(t *testing.T, stateDir string, claim *storage.FinishClaim) {
	t.Helper()
	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storage.FinishClaimPath(stateDir, claim.TrackerID), data, 0644); err != nil {
		t.Fatal(err)
	}
}
