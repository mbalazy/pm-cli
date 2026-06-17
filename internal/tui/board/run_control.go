package board

import (
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
	pid := st.PID
	taskID := st.TaskID
	proj := st.Project
	// Errors here are intentionally dropped: this is a TUI (bubbletea owns the
	// screen, so stderr would corrupt the render), and the SIGTERM may legitimately
	// fail because the process is already gone - the execKillCheckMsg follow-up
	// re-checks liveness and escalates to SIGKILL if needed.
	_ = st.Kill(syscall.SIGTERM)

	// Re-read the freshest run-state (the manager may have advanced it), then
	// stamp it stopped + park the in-flight sub.
	if fresh, err := storage.ReadRunState(stateDir, taskID); err == nil {
		st = fresh
	}
	st.Status = storage.RunStatusFailed
	if st.Error == "" {
		st.Error = "stopped by user"
	}
	inFlight := st.CurrentSub
	if inFlight == "" {
		inFlight = st.TaskID // `pm work`: the task itself is the worker
	}
	for i := range st.Subs {
		if st.Subs[i].ID == inFlight && (st.Subs[i].Status == storage.RunStatusRunning || st.Subs[i].Status == "pending") {
			st.Subs[i].Status = "blocked"
			if st.Subs[i].Note == "" {
				st.Subs[i].Note = "stopped by user"
			}
		}
	}
	_ = storage.WriteRunState(stateDir, st) // best-effort observability (see above)

	// Park the in-flight task on `waiting` so the board reflects the stop.
	if inFlight != "" {
		if t, err := m.store.FindTask(proj, inFlight); err == nil && t.Meta.Status != storage.StatusWaiting {
			m.store.MoveTask(t, storage.StatusWaiting)
		}
	}
	m.reload()
	m.refreshRunStates()
	m.toastMsg = "Stopped executor run " + taskID + " (parked " + inFlight + " on waiting)"
	m.toastExpiry = time.Now().Add(5 * time.Second)
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
