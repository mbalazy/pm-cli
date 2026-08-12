package board

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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
	// Two SEPARATE live processes: with one pid shared between them, "the run is
	// spared" could not tell a kill that signalled only the acceptance from one
	// that signalled both.
	runPID, finPID := liveRunPID(t), liveRunPID(t)
	run := liveRun(t, "p-9", runPID)
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}
	fin := liveFinish(t, "p-9", finPID)
	if err := storage.WriteRunState(stateDir, fin); err != nil {
		t.Fatal(err)
	}
	// The claim the killed process holds: same host, same pid.
	claim, err := storage.AcquireFinishClaim(stateDir, "p-9", "sess-finish")
	if err != nil {
		t.Fatal(err)
	}
	claim.PID = finPID
	writeClaimFixture(t, stateDir, claim)

	cmd := m.killRun(fin)

	got, err := storage.ReadFinishRunState(stateDir, "p-9")
	if err != nil {
		t.Fatalf("acceptance run-state: %v", err)
	}
	if got.Status != storage.RunStatusFailed || got.Kind != storage.RunKindFinish {
		t.Errorf("the acceptance must be stamped failed in its OWN file: %+v", got)
	}
	// "blocked" is an acceptance VERDICT ("it looked and refused"); a run
	// stopped by hand reached no verdict at all, so it is "failed" - the same
	// word `pm finish` records for a worker that died without one.
	if len(got.Subs) != 1 || got.Subs[0].Status != storage.RunStatusFailed {
		t.Errorf("a stopped acceptance sub must be failed, not blocked: %+v", got.Subs)
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
	if !ok || msg.kind != storage.RunKindFinish || msg.pid != finPID {
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
	claim.PID = fin.PID // even the same pid must not be enough - the session is
	writeClaimFixture(t, stateDir, claim)

	m.killRun(fin)

	held, err := storage.ReadFinishClaim(stateDir, "p-9")
	if err != nil || held == nil || held.Session != "somebody-else" {
		t.Errorf("a claim this run never took must survive the kill: %+v err=%v", held, err)
	}
	// And the toast must not claim a release that never happened - the same
	// lie the park branch was fixed for.
	if strings.Contains(m.toastMsg, "(claim released)") {
		t.Errorf("toastMsg = %q, want it to say the claim was not ours", m.toastMsg)
	}
	if !strings.Contains(m.toastMsg, "not ours to release") {
		t.Errorf("toastMsg = %q, want it to name what did NOT happen", m.toastMsg)
	}
}

// Run-states are never deleted, so the ordinary morning state is a FINISHED run
// with a live acceptance beside it. W and K must both land on the acceptance
// there - preferring the run unconditionally would put a dead transcript on
// screen and leave the only live process unkillable from the board.
func TestADeadRunDoesNotShadowALiveAcceptance(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	done := liveRun(t, "p-9", 999999999)
	done.Status = storage.RunStatusDone
	if err := storage.WriteRunState(stateDir, done); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunState(stateDir, liveFinish(t, "p-9", liveRunPID(t))); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}

	st, finish := m.pickRunForTask(task)
	if !finish || st == nil || st.Kind != storage.RunKindFinish {
		t.Fatalf("the live acceptance must win over a finished run: %+v (finish=%v)", st, finish)
	}
	if !m.openExecutorView(task) {
		t.Fatal("openExecutorView returned false")
	}
	if !m.watchingFinish() {
		t.Error("W opened the finished run instead of the live acceptance")
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
	if got, finish := m.pickRunForTask(task); got == nil || got.Kind != storage.RunKindEpic || finish {
		t.Errorf("with both live, K must target the run: %+v (finish=%v)", got, finish)
	}

	// Run finished, acceptance still going: K now means the acceptance.
	run.Status = storage.RunStatusDone
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	if got, finish := m.pickRunForTask(task); got == nil || got.Kind != storage.RunKindFinish || !finish {
		t.Errorf("with only the acceptance live, K must target it: %+v (finish=%v)", got, finish)
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
		t.Fatal("expected the acceptance to be on screen")
	}
	if m.executorHasOther {
		t.Error("there is no run to switch to")
	}

	m.switchExecutorRunKind()
	if !m.watchingFinish() {
		t.Error("the view switched to a run that does not exist")
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
	m.executorWatchFinish = true
	m.confirmAction = "kill-run"
	if got := m.viewExecutor(); !strings.Contains(got, "releases the acceptance claim") || strings.Contains(got, "parks the worker") {
		t.Errorf("confirm footer must describe the acceptance's own effect:\n%s", got)
	}
	m.executorWatchFinish = false
	if got := m.viewExecutor(); !strings.Contains(got, "parks the worker") {
		t.Errorf("a run's confirm footer is unchanged:\n%s", got)
	}
}

// pressKey sends one key through a view's update func and returns the model that
// came back - the update funcs take a value receiver, so the new state arrives
// in the message rather than through the pointer.
func pressKey(t *testing.T, m *Model, r rune, up func(Model, tea.KeyMsg) (tea.Model, tea.Cmd)) *Model {
	t.Helper()
	next, _ := up(*m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	v := next.(Model)
	return &v
}

// A confirmation is given for ONE of the two runs. Inside the agent-view they
// are a single W apart and share a task id, so an armed K must never be spent on
// whatever the view switched to in between. Driven through the KEYS, because
// which of the two mechanisms disarms it - this view's blanket "any key but K
// cancels", or the kind-keyed check in the K branch - is the thing under test.
func TestAgentViewKillConfirmDoesNotCarryAcrossTheSwitch(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	// Two separate live processes, as in the kill tests above: with one pid
	// shared, a regression would signal the group both states name and the
	// failure could not say which run was killed.
	if err := storage.WriteRunState(stateDir, liveRun(t, "p-9", liveRunPID(t))); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteRunState(stateDir, liveFinish(t, "p-9", liveRunPID(t))); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if !m.openExecutorView(task) {
		t.Fatal("openExecutorView returned false with two run-states on disk")
	}

	// Arm K on the run...
	m = pressKey(t, m, 'K', Model.updateExecutorView)
	if m.confirmAction != "kill-run" || m.confirmRunFinish {
		t.Fatalf("K on the run must arm a confirmation for the RUN: %q finish=%v", m.confirmAction, m.confirmRunFinish)
	}

	// ...press W to switch to the acceptance. W is not K, so the view's blanket
	// cancel drops the confirmation before the switch even runs.
	m = pressKey(t, m, 'W', Model.updateExecutorView)
	if !m.watchingFinish() {
		t.Fatal("W did not switch the view to the acceptance")
	}
	if m.confirmAction != "" {
		t.Errorf("switching away must leave no armed kill, got %q", m.confirmAction)
	}

	// ...so the next K only re-arms, this time for the acceptance, and the
	// acceptance's run-state is untouched on disk.
	m = pressKey(t, m, 'K', Model.updateExecutorView)
	if m.confirmAction != "kill-run" || !m.confirmRunFinish {
		t.Fatalf("K after the switch must arm for the ACCEPTANCE: %q finish=%v", m.confirmAction, m.confirmRunFinish)
	}
	fin, err := storage.ReadFinishRunState(stateDir, "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if fin.Status != storage.RunStatusRunning {
		t.Errorf("the acceptance was killed on a confirmation given for the run: status = %q", fin.Status)
	}
}

// The route that actually reaches the kind-keyed check: the DETAIL view has no
// blanket cancel, so a K armed there survives the trip into the agent-view. Arm
// it against the run, let the run finish, and W then opens the acceptance -
// where a single K would otherwise stop it on a confirmation given for
// something else.
func TestKillConfirmArmedInDetailDoesNotKillTheAcceptance(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	run := liveRun(t, "p-9", liveRunPID(t))
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}
	fin := liveFinish(t, "p-9", liveRunPID(t))
	if err := storage.WriteRunState(stateDir, fin); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}
	m.currentView = viewDetail
	m.detailTask = task

	// K in the detail view arms against the RUN: it is live, and a live run wins
	// the tie with a live acceptance.
	m = pressKey(t, m, 'K', Model.updateDetail)
	if m.confirmAction != "kill-run" || m.confirmRunFinish {
		t.Fatalf("K in the detail view must arm for the RUN: %q finish=%v", m.confirmAction, m.confirmRunFinish)
	}

	// The run ends while the confirmation stands, so the acceptance is now the
	// only live thing on this tracker - and what W opens.
	run.Status = storage.RunStatusDone
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	if !m.openExecutorView(task) || !m.watchingFinish() {
		t.Fatalf("W must open the acceptance once the run is done (finish=%v)", m.watchingFinish())
	}
	if m.confirmAction != "kill-run" {
		t.Fatal("nothing on this route disarms the confirmation - the test would prove nothing")
	}

	// One K here must RE-ARM, not fire.
	m = pressKey(t, m, 'K', Model.updateExecutorView)
	if !m.confirmRunFinish {
		t.Errorf("K must re-arm against the acceptance, got finish=%v", m.confirmRunFinish)
	}
	got, err := storage.ReadFinishRunState(stateDir, "p-9")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != storage.RunStatusRunning {
		t.Errorf("the acceptance was stopped on a confirmation armed against the run: status = %q", got.Status)
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

// A hand-driven acceptance (a CC session that took the claim via `pm finish
// claim` - batch-finish-auto's shape) writes no .finish.json, so the claim is
// its ONLY artifact. The board must show it on the card and in the detail
// view for exactly as long as the claim is live - and hand the screen back to
// the .finish.json dashboard the moment a real acceptance process runs.
func TestHandClaimedAcceptanceIsVisibleUntilTheClaimExpires(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	if _, err := storage.AcquireFinishClaim(stateDir, "p-9", "sess-hand"); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()

	if m.finishClaims["p-9"] == nil {
		t.Fatal("refreshRunStates must pick the live claim up on the same tick as the run-states")
	}
	if fb := m.finishBadge("p-9"); fb != "▶ accepting (claimed)" {
		t.Errorf("badge = %q, want the hand-claimed acceptance badged as in progress", fb)
	}
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}
	detail := m.renderTaskDetail(task)
	if !strings.Contains(detail, "Acceptance (hand-claimed)") {
		t.Errorf("detail view must carry the claim notice:\n%s", detail)
	}
	if !strings.Contains(detail, "sess-han") {
		t.Errorf("the notice must name the claiming session:\n%s", detail)
	}

	// A LIVE acceptance process (its .finish.json) wins the tie: that process
	// holds its own claim, and doubling the badge or the notice would show one
	// acceptance as two.
	if err := storage.WriteRunState(stateDir, liveFinish(t, "p-9", liveRunPID(t))); err != nil {
		t.Fatal(err)
	}
	m.refreshRunStates()
	if fb := m.finishBadge("p-9"); fb != "▶ accepting" {
		t.Errorf("badge = %q with a live acceptance process, want the plain live badge", fb)
	}
	if detail := m.renderTaskDetail(task); strings.Contains(detail, "hand-claimed") {
		t.Errorf("the claim notice must yield to the live acceptance dashboard:\n%s", detail)
	}
	if err := os.Remove(storage.FinishRunPath(stateDir, "p-9")); err != nil {
		t.Fatal(err)
	}

	// Expiry: stamps older than the TTL drop the claim on the next tick, and
	// the board goes quiet about the acceptance on its own.
	old := time.Now().Add(-storage.FinishClaimTTL - time.Minute).UTC().Format(time.RFC3339)
	writeClaimFixture(t, stateDir, &storage.FinishClaim{TrackerID: "p-9", Host: storage.Hostname(), Started: old, Refreshed: old})
	m.refreshRunStates()
	if m.finishClaims["p-9"] != nil {
		t.Fatal("an expired claim must not survive the tick")
	}
	if fb := m.finishBadge("p-9"); fb != "" {
		t.Errorf("badge = %q after expiry, want none", fb)
	}
	if detail := m.renderTaskDetail(task); strings.Contains(detail, "hand-claimed") {
		t.Errorf("the claim notice must disappear with the expired claim:\n%s", detail)
	}
}

// The detail view's tick-refresh is gated (a static task must not reload every
// 2s), and a hand-driven acceptance has NO live run-state to open that gate -
// the claim itself must. Both directions matter: a claim appearing while the
// view is open has to draw the notice, and the tick that drops an expired
// claim has to take it off the screen (the live board bug the first visual
// pass of pm-cli-112 caught: the badge vanished, the open detail view kept
// showing the notice until a keypress).
func TestDetailViewTickFollowsTheClaimAlone(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	stateDir := m.store.ProjectDir("p")
	task, err := m.store.FindTask("p", "p-9")
	if err != nil {
		t.Fatal(err)
	}
	m.currentView = viewDetail
	m.detailTask = task
	m.detailViewport = viewport.New(120, 40)
	content := m.renderTaskDetail(task)
	m.detailViewport.SetContent(content)
	m.detailPlainContent = stripANSI(content)
	if strings.Contains(m.detailPlainContent, "hand-claimed") {
		t.Fatal("no claim yet, no notice")
	}

	tick := func() {
		t.Helper()
		nm, _ := m.Update(tickMsg(time.Now()))
		got, ok := nm.(Model)
		if !ok {
			t.Fatalf("Update returned %T", nm)
		}
		*m = got
	}

	// Claim appears while the view is open: the next tick draws the notice.
	if _, err := storage.AcquireFinishClaim(stateDir, "p-9", "sess-hand"); err != nil {
		t.Fatal(err)
	}
	tick()
	if !strings.Contains(m.detailPlainContent, "hand-claimed") {
		t.Fatalf("the tick must draw the claim notice into the open detail view:\n%s", m.detailPlainContent)
	}

	// Claim expires: the SAME tick that drops it from finishClaims must also
	// re-render, or the notice outlives the claim on screen.
	old := time.Now().Add(-storage.FinishClaimTTL - time.Minute).UTC().Format(time.RFC3339)
	writeClaimFixture(t, stateDir, &storage.FinishClaim{TrackerID: "p-9", Host: storage.Hostname(), Started: old, Refreshed: old})
	tick()
	if strings.Contains(m.detailPlainContent, "hand-claimed") {
		t.Fatalf("the notice must leave the screen on the tick the claim expires:\n%s", m.detailPlainContent)
	}
}
