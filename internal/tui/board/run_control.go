package board

import (
	"errors"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
)

// refreshRunStates reloads executor run-states for the visible project(s) into
// m.runStates, and the acceptance run-states into m.finishStates. Cheap (a small
// dir of small JSON files) so it runs on every tick.
func (m *Model) refreshRunStates() {
	states := map[string]*storage.RunState{}
	finish := map[string]*storage.RunState{}
	var slugs []string
	if m.activeProject == 0 {
		slugs = append(slugs, m.projects[1:]...) // "all": every project (skip the "all" pseudo-entry)
	} else if m.activeProject < len(m.projects) {
		slugs = append(slugs, m.projects[m.activeProject])
	}
	for _, slug := range slugs {
		dir := m.store.ProjectDir(slug)
		for id, st := range storage.ReadRunStates(dir) {
			states[id] = st
		}
		// Separate map, separate reader: ReadRunStates and ReadFinishRunStates
		// never return the same file, and the two results must not be poured
		// into one map (see Model.finishStates).
		for id, st := range storage.ReadFinishRunStates(dir) {
			finish[id] = st
		}
	}
	m.runStates = states
	m.finishStates = finish
}

// runForTask returns the executor run-state to act on for task t: its own run,
// else its parent tracker's run. nil when there is none.
func (m Model) runForTask(t *storage.Task) *storage.RunState {
	if t == nil {
		return nil
	}
	if st := m.runStates[t.Meta.ID]; st != nil {
		return st
	}
	if t.Meta.Parent != "" {
		return m.runStates[t.Meta.Parent]
	}
	return nil
}

// finishForTask is runForTask's mirror over the acceptance run-states: task t's
// own acceptance, else the one covering its parent tracker.
func (m Model) finishForTask(t *storage.Task) *storage.RunState {
	if t == nil {
		return nil
	}
	if st := m.finishStates[t.Meta.ID]; st != nil {
		return st
	}
	if t.Meta.Parent != "" {
		return m.finishStates[t.Meta.Parent]
	}
	return nil
}

// pickRunForTask chooses which of task t's two run-states the board acts on -
// for K, and for what W opens - and says WHICH it picked, since the two share a
// task id and nothing downstream could tell them apart otherwise.
//
// A run and its acceptance can be live AT THE SAME TIME (that is the designed
// state, not an exception), so LIVENESS decides first and the run wins a tie:
// it is the one spending a worker on code, and an acceptance outliving it is
// harmless while the reverse is not. Only when neither is live does it fall
// back to the run's own record. Preferring the run unconditionally would be
// wrong the other way round: run-states are never deleted, so the ordinary
// morning state - last night's batch finished, its acceptance running now -
// would put a dead run on screen and out of K's reach.
//
// This ordering is also why the agent-view can kill: with both live, K on a
// card always means the run, so stopping only the acceptance is done by opening
// it (W, then W to switch) and pressing K on what is on screen.
func (m Model) pickRunForTask(t *storage.Task) (*storage.RunState, bool) {
	run, fin := m.runForTask(t), m.finishForTask(t)
	switch {
	case run.IsLive():
		return run, false
	case fin.IsLive():
		return fin, true
	case run != nil:
		return run, false
	}
	return fin, fin != nil
}

// execKillCheckMsg fires a short while after a kill so we can escalate to
// SIGKILL if the process group survived the SIGTERM.
type execKillCheckMsg struct {
	// pid is the manager pid this kill targeted. The escalation re-reads the
	// run-state and refuses to signal when the pid there has moved on - the
	// worker group to signal comes from that fresh state too (it is not in the
	// manager's group - see storage.RunState.WorkerPGID - and it moves with
	// every sub).
	pid     int
	project string
	taskID  string
	// kind routes that re-read to the right FILE. A run and its acceptance share
	// a task id, so without it the escalation of a killed acceptance would read
	// the run's state instead - and, with a live run there, SIGKILL a healthy
	// manager on the strength of somebody else's pid.
	kind string
}

// readRunStateFor re-reads one of a task's two run-states: the acceptance when
// finish is set, the run otherwise. Reading the wrong one is not a miss but a
// confusion, since the two files describe different live processes.
func readRunStateFor(stateDir, taskID string, finish bool) (*storage.RunState, error) {
	if finish {
		return storage.ReadFinishRunState(stateDir, taskID)
	}
	return storage.ReadRunState(stateDir, taskID)
}

// readRunStateOfKind is readRunStateFor for the paths that carry a run's KIND
// rather than a choice (the kill escalation message, killRun's own re-read).
// Any kind but the acceptance's routes to the run's own file - the same rule
// storage.runStatePath writes by.
func readRunStateOfKind(stateDir, taskID, kind string) (*storage.RunState, error) {
	return readRunStateFor(stateDir, taskID, kind == storage.RunKindFinish)
}

// killRun stops the executor run st: SIGTERM to its process group, then stamps
// the run-state stopped and parks the in-flight worker on `waiting` (the manager
// dies before it can write its own end-state). Returns a tea.Cmd that escalates
// to SIGKILL if the group is still alive a couple of seconds later.
func (m *Model) killRun(st *storage.RunState) tea.Cmd {
	if st == nil {
		return nil
	}
	stateDir := m.store.ProjectDir(st.Project)
	taskID := st.TaskID
	proj := st.Project
	kind := st.Kind
	// Re-read the freshest run-state BEFORE signalling: the board's copy is up
	// to a tick old, and WorkerPGID moves with every sub - signalling a stale
	// one would miss the live worker (and, once its pid is recycled, could
	// reach an unrelated group). Routed by KIND, so killing an acceptance never
	// reads (nor, below, writes over) the run-state of the run it accepts.
	if fresh, err := readRunStateOfKind(stateDir, taskID, kind); err == nil {
		st = fresh
		// Read by kind, so write back by the same kind: WriteRunState routes on
		// st.Kind, and a state whose kind disagreed with the file it came out of
		// would be stamped over the OTHER run's file.
		st.Kind = kind
	}
	acceptance := kind == storage.RunKindFinish
	pid := st.PID
	// A signal error is intentionally dropped: this is a TUI (bubbletea owns the
	// screen, so stderr would corrupt the render), and the SIGTERM may legitimately
	// fail because the process is already gone - the execKillCheckMsg follow-up
	// re-checks and escalates to SIGKILL if needed.
	//
	// A STALE run-state is different from a failed signal, and is the one case
	// that must not be silent: nothing was signalled because the pid could no
	// longer be shown to be this run's process (crashed run, reboot, recycled
	// number). The state below is still reconciled - that is the honest outcome
	// - but the user is told the run was already gone rather than that it was
	// stopped, and no SIGKILL is scheduled against a pid pm just refused to
	// signal.
	var stale *storage.StaleRunError
	alreadyGone := errors.As(st.Kill(syscall.SIGTERM), &stale)

	// From here on st is the freshest state; stamp it stopped + park the
	// in-flight sub.
	stopNote := "stopped by user"
	if alreadyGone {
		stopNote = "stopped by user - the run was already gone, nothing was signalled"
	}
	st.Status = storage.RunStatusFailed
	if st.Error == "" {
		st.Error = stopNote
	}
	inFlight := st.CurrentSub
	if inFlight == "" {
		inFlight = st.TaskID // `pm work`: the task itself is the worker
	}
	// What a stopped worker's entry says it is. For an acceptance this is
	// "failed", never its own "blocked" verdict: `blocked` in an acceptance's
	// sub means "it looked at the work and refused it", and a run stopped by
	// hand reached no verdict at all - `pm finish` records "failed" on exactly
	// this branch (a worker that died without a verdict) for the same reason.
	stoppedSub := "blocked"
	if acceptance {
		stoppedSub = storage.RunStatusFailed
	}
	for i := range st.Subs {
		if st.Subs[i].ID == inFlight && (st.Subs[i].Status == storage.RunStatusRunning || st.Subs[i].Status == "pending") {
			st.Subs[i].Status = stoppedSub
			if st.Subs[i].Note == "" {
				st.Subs[i].Note = stopNote
			}
		}
	}
	_ = storage.WriteRunState(stateDir, st) // best-effort observability (see above)

	// Journal the kill on the dead manager's behalf - its own "end" line never
	// runs (the defer died with the process). With this, a "start" line with
	// no "end"/"killed" line means an untracked crash, which the retro flow
	// can surface separately from deliberate stops. Per-sub outcomes so far
	// live in the re-read run-state subs (st.Subs, just updated above) - carry
	// them into the journal so a killed epic still counts its already-merged
	// subs towards the histogram/turns/cost totals instead of contributing
	// nothing despite real token spend.
	subs := make([]storage.JournalSub, 0, len(st.Subs))
	for _, sr := range st.Subs {
		subs = append(subs, storage.JournalSub{
			ID: sr.ID, Result: sr.Status, Note: sr.Note, Session: sr.Session,
			Turns: sr.Turns, CostUSD: sr.CostUSD,
		})
	}
	durationS := 0
	if started, err := time.Parse(time.RFC3339, st.Started); err == nil {
		durationS = int(time.Since(started).Seconds())
	}
	_ = storage.AppendJournal(stateDir, &storage.JournalEntry{
		// RunID off the re-read run-state: it makes this line provably the same
		// physical run the manager started, so `pm executor stats` never has to
		// guess whether it is the manager's own "end" line raced (one run) or a
		// second run that reused the pid. A pre-0.27.0 manager wrote no id -
		// the entry then falls back to {kind, task_id, pid} pairing.
		Event: storage.JournalEventKilled, Kind: st.Kind, Project: proj, TaskID: taskID, RunID: st.RunID,
		PID: pid, Status: storage.RunStatusFailed, Error: stopNote,
		Subs: subs, DurationS: durationS,
	})

	// Release the worktree lock the killed manager held. The SIGTERM'd process
	// dies before its own deferred release runs, so free "additional" here on its
	// behalf (PID-matched, so we only drop a lock this run actually owned). A
	// non-worktree run has no lock at RepoPath -> no-op. Belt-and-suspenders with
	// stale-lock recovery on the next run.
	if st.RepoPath != "" {
		_ = storage.ReleaseWorktreeLock(st.RepoPath, pid)
	}

	// Same job for the acceptance claim, and for the same reason: the killed
	// `pm finish` never runs its own deferred release, so without this the run
	// stays locked against the next acceptance for the rest of the TTL - and a
	// claim naming a pid that is already gone is exactly what gets a lock file
	// deleted by hand.
	claimReleased := false
	if acceptance {
		claimReleased = releaseKilledFinishClaim(stateDir, taskID, st)
	}

	// Park the in-flight task on `waiting` so the board reflects the stop.
	//
	// Never for an acceptance: its one "sub" IS the tracker, and pm's rule is
	// that the executor neither closes nor moves a parent tracker. Killing an
	// acceptance says nothing about the state of the work it was accepting, so
	// dragging the tracker to `waiting` would be a status change the user never
	// asked for and did not cause.
	var parkErr error
	if inFlight != "" && !acceptance {
		if t, err := m.store.FindTask(proj, inFlight); err == nil && t.Meta.Status != storage.StatusWaiting {
			parkErr = m.store.MoveTask(t, storage.StatusWaiting)
		}
	}
	m.reload()
	m.refreshRunStates()
	noun := "executor run "
	if acceptance {
		noun = "acceptance run "
	}
	verb := "Stopped " + noun
	if alreadyGone {
		verb = "Run already gone (nothing signalled), state reconciled: "
	}
	switch {
	case parkErr != nil:
		// MoveTask can legally fail (e.g. the task file was deleted while the
		// run was live) - the old unconditional toast lied about the park.
		m.showErrorToast(verb+taskID+", but failed to park "+inFlight, parkErr)
	case acceptance:
		// No park to report, and saying so would invent one. The claim is
		// reported only when it was actually released: a claim held by another
		// host, or by an acceptance that took the run over, is left alone (see
		// releaseKilledFinishClaim), and a toast that announced a release either
		// way would be the same lie the park branch above was fixed for.
		note := " (its claim was not ours to release)"
		if claimReleased {
			note = " (claim released)"
		}
		m.toastMsg = verb + taskID + note
		m.toastExpiry = time.Now().Add(5 * time.Second)
	default:
		m.toastMsg = verb + taskID + " (parked " + inFlight + " on waiting)"
		m.toastExpiry = time.Now().Add(5 * time.Second)
	}
	if alreadyGone {
		// Nothing was signalled, so there is nothing to escalate against - and
		// scheduling one would only re-open the question of whose pid that is.
		return nil
	}
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return execKillCheckMsg{pid: pid, project: proj, taskID: taskID, kind: kind}
	})
}

// releaseKilledFinishClaim drops the acceptance claim held by the run this kill
// just stopped, on its behalf, and reports whether it actually did.
//
// The question here is not `pm finish release`'s ("is this claim mine") but "is
// this claim THE KILLED RUN'S", and the identifier that answers it is the
// SESSION: `pm finish` mints one id per run and stamps it on both artifacts -
// into the claim it acquires and onto the run-state it writes - so a match is
// proof, and the two are only ever written together. The pid deliberately is
// not the test: pids get recycled, and this runs on the stale path too, where
// the pid has just been shown NOT to belong to this run. A claim carrying no
// session, or another host's, or a later acceptance's that took the run over,
// is left exactly where it is; it expires on its own within the TTL.
//
// Failures are silent for the same reason every other write in killRun is: this
// is a TUI, and bubbletea owns the screen. The BOOLEAN is what the caller says
// out loud instead.
func releaseKilledFinishClaim(stateDir, tracker string, st *storage.RunState) bool {
	claim, err := storage.ReadFinishClaim(stateDir, tracker)
	if err != nil || claim == nil || claim.Session == "" {
		return false
	}
	if claim.Host != storage.Hostname() || !runHasSession(st, claim.Session) {
		return false
	}
	return storage.ReleaseFinishClaim(stateDir, tracker, claim) == nil
}

// runHasSession reports whether session is one this run recorded. Both places
// are checked because they have different lifetimes: CurrentSession is cleared
// the moment the worker returns, while the sub's copy persists for the life of
// the run-state.
func runHasSession(st *storage.RunState, session string) bool {
	if st == nil || session == "" {
		return false
	}
	if st.CurrentSession == session {
		return true
	}
	for _, s := range st.Subs {
		if s.Session == session {
			return true
		}
	}
	return false
}

// runBadge returns a compact card badge for an executor run on taskID, or "".
// A finished (done) run is intentionally not badged - the task's own status
// already moved; only active/attention-worthy runs are surfaced.
func (m Model) runBadge(taskID string) string {
	return stateBadge(m.runStates[taskID], "▶ running", "▷ stopped", "✗ run failed")
}

// finishBadge is runBadge's counterpart for the acceptance of a run. It is a
// SEPARATE badge, rendered alongside rather than instead: a card can carry both
// at once, because a run and its acceptance are routinely live together.
func (m Model) finishBadge(taskID string) string {
	return stateBadge(m.finishStates[taskID], "▶ accepting", "▷ accept stopped", "✗ accept failed")
}

// stateBadge is the shared shape of both badges: live, marked-running-but-gone,
// failed - and nothing at all for a run that finished cleanly (the work's own
// status has moved on by then).
func stateBadge(st *storage.RunState, live, stopped, failed string) string {
	if st == nil {
		return ""
	}
	switch {
	case st.IsLive():
		return live
	case st.Status == storage.RunStatusRunning: // marked running but the process is gone
		return stopped
	case st.Status == storage.RunStatusFailed:
		return failed
	}
	return ""
}

// confirmRunLabel names the run a pending kill confirmation targets. The board
// and the detail view both ask "press K again to stop the ...", and with a run
// and its acceptance one keystroke apart the sentence has to say which.
func confirmRunLabel(finish bool) string {
	if finish {
		return "acceptance run"
	}
	return "executor run"
}
