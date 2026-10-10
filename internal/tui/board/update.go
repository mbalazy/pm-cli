package board

import (
	"fmt"
	"regexp"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/tui/common"
)

var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func stripANSI(s string) string {
	return ansiRegexp.ReplaceAllString(s, "")
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.detailViewport.Width = msg.Width
		m.detailViewport.Height = msg.Height - 2
		m.executorViewport.Width = msg.Width
		m.executorViewport.Height = executorBodyHeight(msg.Height)
		m.reportViewport.Width = msg.Width
		m.reportViewport.Height = reportBodyHeight(msg.Height)
		// The report is markdown WRAPPED at render time, so unlike the viewports
		// above it does not re-flow by being told its new width - it has to be
		// rendered again at it.
		if m.currentView == viewReport {
			m.reloadReport()
		}
		// The Runs view scrolls on the terminal height, so a resize can leave its
		// cursor off-screen until the next keypress.
		m.fixRunsCursor()
		return m, nil

	case tickMsg:
		// No periodic task reload - the board refreshes on demand via `r`. The
		// tick keeps the render loop alive (toasts expire) and refreshes the
		// cheap executor run-states so live runs show up without a manual reload.
		m.refreshRunStates()
		// Live-refresh the slot indicator while the executor launch menu is open,
		// so a slot freed/claimed mid-decision shows up without reopening (two
		// small lock-file reads per slot).
		if m.claudeMenu && m.launchAgent == launchAgentExecutor && m.executorAdditionalAvail && m.store != nil {
			if t := m.menuTask(); t != nil {
				if proj, err := m.store.GetProject(t.Project); err == nil {
					m.executorSlots = executorSlotStatuses(proj)
				}
			}
		}
		if m.currentView == viewExecutor {
			m.refreshExecutorView()
		}
		// Only while the Runs view is open: unlike refreshRunStates above, this
		// reads EVERY project's tasks and run-states, which the board itself has no
		// use for (see refreshRunsView). Remote rows are NOT re-fetched on a tick -
		// that is `f`.
		if m.currentView == viewRuns {
			m.refreshRunsView()
		}
		// Live-refresh the task-detail view while a run for it is active: reload
		// tasks (so the subtask table + parent rollup reflect fresh sub statuses)
		// and re-render the dashboard, preserving scroll.
		if m.currentView == viewDetail && m.detailTask != nil {
			// Either dashboard being live is reason enough to re-render: an
			// acceptance can be the only thing still moving on a finished run.
			// A live CLAIM counts the same - a hand-driven acceptance has no
			// run-state, so the claim is all that moves (it appears, its ages
			// tick, it expires). detailHadClaim buys the ONE render after the
			// claim goes: refreshRunStates above has already dropped it from
			// finishClaims by the time this condition runs, so without the
			// memory the notice would sit on screen until a keypress.
			claimLive := m.finishClaims[m.detailTask.Meta.ID] != nil
			if m.runStates[m.detailTask.Meta.ID].IsLive() || m.finishStates[m.detailTask.Meta.ID].IsLive() || claimLive || m.detailHadClaim {
				m.reload()
				if rt, err := m.store.FindTask(m.detailTask.Project, m.detailTask.Meta.ID); err == nil {
					m.detailTask = rt
				}
				off := m.detailViewport.YOffset
				content := m.renderTaskDetail(m.detailTask)
				m.detailViewport.SetContent(content)
				m.detailPlainContent = stripANSI(content)
				m.detailViewport.SetYOffset(off)
			}
			m.detailHadClaim = claimLive
		}
		return m, doTick()

	case execKillCheckMsg:
		// The group didn't die on SIGTERM - escalate to SIGKILL, through the same
		// verified path as the SIGTERM (RunState.Kill: identity first, then the
		// worker's own group before the manager's, since SIGKILLing the manager is
		// what makes the worker unreachable).
		//
		// The state is re-read rather than trusted from the message: two seconds
		// is ample for the manager to exit and, after a crash or a reboot, for
		// its number to belong to something else - and a SIGKILL cannot be taken
		// back. No fresh state, or a state whose pid has moved on to another run,
		// means no proof, which means no signal. Errors are dropped: the process
		// may legitimately have exited in the gap (the next refreshRunStates
		// reflects reality), and this is a TUI so stderr is unusable.
		if st, err := readRunStateOfKind(m.store.ProjectDir(msg.project), msg.taskID, msg.kind); err == nil && st.PID == msg.pid {
			_ = st.Kill(syscall.SIGKILL)
		}
		m.refreshRunStates()
		return m, nil

	case runsRemoteMsg:
		m.applyRemoteRuns(msg)
		// See reloadMsg below: the tick loop is already running, don't fan it out.
		return m, nil

	case reloadMsg:
		// No doTick() here: the tickMsg loop started by Init already runs for
		// the life of the process. Re-issuing it here used to add a PERMANENT
		// extra 2s loop for every editor return / launch return, compounding
		// unboundedly over a session.
		m.reload()
		return m, nil

	case launchResultMsg:
		m.reload()
		if msg.err != nil {
			m.toastMsg = fmt.Sprintf("%s %s failed: %v", msg.agent.label(), msg.kind, msg.err)
		} else {
			m.toastMsg = fmt.Sprintf("%s %s exited", msg.agent.label(), msg.kind)
		}
		m.toastExpiry = time.Now().Add(15 * time.Second)
		// See reloadMsg above: the tick loop is already running, don't fan it out.
		return m, nil

	case editorResultMsg:
		m.reload()
		if msg.err != nil {
			m.showErrorToast("editor failed", msg.err)
		}
		// See reloadMsg above: the tick loop is already running, don't fan it out.
		return m, nil

	case tea.MouseMsg:
		if m.currentView == viewDetail || m.currentView == viewProjectInfo {
			var cmd tea.Cmd
			m.detailViewport, cmd = m.detailViewport.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		// An open overlay owns the keyboard, and which one wins is decided in
		// ONE place (overlayLadder) shared with View - so the layer the user
		// is looking at is always the layer handling the key.
		if ov, ok := m.activeOverlay(); ok {
			return ov.update(m, msg)
		}
		if m.currentView == viewExecutor {
			return m.updateExecutorView(msg)
		}
		if m.currentView == viewDetail || m.currentView == viewProjectInfo {
			return m.updateDetail(msg)
		}
		if m.currentView == viewArchive {
			return m.updateArchive(msg)
		}
		if m.currentView == viewFocus {
			return m.updateFocus(msg)
		}
		if m.currentView == viewRuns {
			return m.updateRuns(msg)
		}
		if m.currentView == viewReport {
			return m.updateReportView(msg)
		}
		if m.adding {
			return m.updateAdd(msg)
		}
		if m.searching {
			return m.updateSearch(msg)
		}
		if m.selecting {
			return m.updateSelectMode(msg)
		}
		return m.updateBoard(msg)
	}
	return m, nil
}

// updateBoard is the board view's keyboard: the confirmation reset first,
// then the key table (boardKeys, update_board.go), then the 1-9 project jump.
func (m Model) updateBoard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.dropStaleConfirm(msg)
	for _, k := range boardKeys {
		if k.matches(msg) {
			// Two statements on purpose: in `return m, k.handle(&m)` the read
			// of m and the call are unordered by the spec.
			cmd := k.handle(&m)
			return m, cmd
		}
	}
	m.jumpToProjectKey(msg)
	return m, nil
}

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Enter):
		m.searching = false
		m.searchQuery = m.searchInput.Value()
		m.searchInput.Blur()
		m.fixCursors()
	case key.Matches(msg, common.Keys.Escape):
		m.searching = false
		m.searchQuery = ""
		m.searchInput.SetValue("")
		m.searchInput.Blur()
		m.fixCursors()
	default:
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		m.searchQuery = m.searchInput.Value()
		m.fixCursors()
		return m, cmd
	}
	return m, nil
}
