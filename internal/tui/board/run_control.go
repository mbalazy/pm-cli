package board

import (
	"errors"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
)

// refreshRunStates reloads executor run-states for the visible project(s) into
// m.runStates. Cheap (a small dir of small JSON files) so it runs on every tick.
func (m *Model) refreshRunStates() {
	states := map[string]*storage.RunState{}
	var slugs []string
	if m.activeProject == 0 {
		slugs = append(slugs, m.projects[1:]...) // "all": every project (skip the "all" pseudo-entry)
	} else if m.activeProject < len(m.projects) {
		slugs = append(slugs, m.projects[m.activeProject])
	}
	for _, slug := range slugs {
		for id, st := range storage.ReadRunStates(m.store.ProjectDir(slug)) {
			states[id] = st
		}
	}
	m.runStates = states
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
	// Re-read the freshest run-state BEFORE signalling: the board's copy is up
	// to a tick old, and WorkerPGID moves with every sub - signalling a stale
	// one would miss the live worker (and, once its pid is recycled, could
	// reach an unrelated group).
	if fresh, err := storage.ReadRunState(stateDir, taskID); err == nil {
		st = fresh
	}
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
	for i := range st.Subs {
		if st.Subs[i].ID == inFlight && (st.Subs[i].Status == storage.RunStatusRunning || st.Subs[i].Status == "pending") {
			st.Subs[i].Status = "blocked"
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

	// Park the in-flight task on `waiting` so the board reflects the stop.
	var parkErr error
	if inFlight != "" {
		if t, err := m.store.FindTask(proj, inFlight); err == nil && t.Meta.Status != storage.StatusWaiting {
			parkErr = m.store.MoveTask(t, storage.StatusWaiting)
		}
	}
	m.reload()
	m.refreshRunStates()
	verb := "Stopped executor run "
	if alreadyGone {
		verb = "Run already gone (nothing signalled), state reconciled: "
	}
	if parkErr != nil {
		// MoveTask can legally fail (e.g. the task file was deleted while the
		// run was live) - the old unconditional toast lied about the park.
		m.showErrorToast(verb+taskID+", but failed to park "+inFlight, parkErr)
	} else {
		m.toastMsg = verb + taskID + " (parked " + inFlight + " on waiting)"
		m.toastExpiry = time.Now().Add(5 * time.Second)
	}
	if alreadyGone {
		// Nothing was signalled, so there is nothing to escalate against - and
		// scheduling one would only re-open the question of whose pid that is.
		return nil
	}
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return execKillCheckMsg{pid: pid, project: proj, taskID: taskID}
	})
}

// runBadge returns a compact card badge for an executor run on taskID, or "".
// A finished (done) run is intentionally not badged - the task's own status
// already moved; only active/attention-worthy runs are surfaced.
func (m Model) runBadge(taskID string) string {
	st := m.runStates[taskID]
	if st == nil {
		return ""
	}
	switch {
	case st.IsLive():
		return "▶ running"
	case st.Status == storage.RunStatusRunning: // marked running but the process is gone
		return "▷ stopped"
	case st.Status == storage.RunStatusFailed:
		return "✗ run failed"
	}
	return ""
}
