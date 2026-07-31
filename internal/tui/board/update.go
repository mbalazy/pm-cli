package board

import (
	"fmt"
	"regexp"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
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
		// Live-refresh the task-detail view while a run for it is active: reload
		// tasks (so the subtask table + parent rollup reflect fresh sub statuses)
		// and re-render the dashboard, preserving scroll.
		if m.currentView == viewDetail && m.detailTask != nil {
			if run := m.runStates[m.detailTask.Meta.ID]; run != nil && run.IsLive() {
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
		}
		return m, doTick()

	case execKillCheckMsg:
		// The group didn't die on SIGTERM - escalate to SIGKILL (group, then pid).
		// Errors are dropped: the process may have exited in the gap (the next
		// refreshRunStates reflects reality), and this is a TUI so stderr is unusable.
		if storage.ProcessAlive(msg.pid) {
			// The worker's own group first: SIGKILLing the manager is what makes
			// the worker unreachable (it can no longer forward), so it must not
			// happen before the worker itself is down.
			if msg.workerPGID > 0 && msg.workerPGID != msg.pid {
				_ = syscall.Kill(-msg.workerPGID, syscall.SIGKILL)
			}
			_ = syscall.Kill(-msg.pid, syscall.SIGKILL)
			_ = syscall.Kill(msg.pid, syscall.SIGKILL)
		}
		m.refreshRunStates()
		return m, nil

	case reloadMsg:
		m.reload()
		return m, doTick()

	case launchResultMsg:
		m.reload()
		if msg.err != nil {
			m.toastMsg = fmt.Sprintf("%s %s failed: %v", msg.agent.label(), msg.kind, msg.err)
		} else {
			m.toastMsg = fmt.Sprintf("%s %s exited", msg.agent.label(), msg.kind)
		}
		m.toastExpiry = time.Now().Add(15 * time.Second)
		return m, doTick()

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

func (m Model) updateBoard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Confirmation pattern: any key other than the confirming key resets
	if m.confirmAction != "" {
		isConfirmKey := false
		switch m.confirmAction {
		case "done":
			isConfirmKey = key.Matches(msg, common.Keys.Done)
		case "waiting":
			isConfirmKey = key.Matches(msg, common.Keys.Waiting)
		case "delete":
			isConfirmKey = key.Matches(msg, common.Keys.Delete)
		case "archive":
			isConfirmKey = key.Matches(msg, common.Keys.Archive)
		case "quit":
			isConfirmKey = key.Matches(msg, common.Keys.Quit)
		case "kill-run":
			isConfirmKey = key.Matches(msg, common.Keys.KillRun)
		}
		if !isConfirmKey {
			m.confirmAction = ""
			m.confirmTaskID = ""
			// fall through to normal handling
		}
	}

	switch {
	case key.Matches(msg, common.Keys.Quit):
		if m.confirmAction == "quit" {
			return m, tea.Quit
		}
		m.confirmAction = "quit"

	case key.Matches(msg, common.Keys.Left):
		if len(m.statuses) > 0 {
			m.activeCol = (m.activeCol - 1 + len(m.statuses)) % len(m.statuses)
		}

	case key.Matches(msg, common.Keys.Right):
		if len(m.statuses) > 0 {
			m.activeCol = (m.activeCol + 1) % len(m.statuses)
		}

	case key.Matches(msg, common.Keys.Up):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 && m.cursors[m.activeCol] > 0 {
			m.cursors[m.activeCol]--
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 && m.cursors[m.activeCol] < len(tasks)-1 {
			m.cursors[m.activeCol]++
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.JumpTop):
		// With every column hidden m.cursors is empty and m.activeCol is 0 -
		// unguarded indexing here panicked the whole board.
		if len(m.statuses) > 0 {
			m.cursors[m.activeCol] = 0
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = len(tasks) - 1
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.HalfDown):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = min(m.cursors[m.activeCol]+5, len(tasks)-1)
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.HalfUp):
		if len(m.statuses) > 0 {
			m.cursors[m.activeCol] = max(m.cursors[m.activeCol]-5, 0)
			m.fixScrollOffsets()
		}

	case msg.String() == "r":
		m.refreshProjects()
		m.reload()
		m.toastMsg = "Refreshed"
		m.toastExpiry = time.Now().Add(2 * time.Second)

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = m.nextVisibleProject(1)
		m.reload()

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = m.nextVisibleProject(-1)
		m.reload()

	case key.Matches(msg, common.Keys.MoveBack):
		if t := m.selectedTask(); t != nil {
			m.doMoveBack(t)
		}

	case key.Matches(msg, common.Keys.Move):
		if t := m.selectedTask(); t != nil {
			m.doMoveForward(t)
		}

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		t := m.selectedTask()
		if t != nil {
			m.previousView = viewBoard
			m.currentView = viewDetail
			m.openDetailTask(t)
		}

	case key.Matches(msg, common.Keys.Edit):
		t := m.selectedTask()
		if t != nil {
			return m, openEditor(t.FilePath)
		}

	case key.Matches(msg, common.Keys.Done):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if m.confirmAction == "done" && m.confirmTaskID == t.Meta.ID {
			m.doDone(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
		} else {
			m.confirmAction = "done"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Waiting):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if m.confirmAction == "waiting" && m.confirmTaskID == t.Meta.ID {
			m.doWaiting(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
		} else {
			m.confirmAction = "waiting"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Delete):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if m.confirmAction == "delete" && m.confirmTaskID == t.Meta.ID {
			// confirmed
			m.lastUndo = &undoAction{kind: "delete", task: snapshotTask(t)}
			m.store.DeleteTask(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
			m.reload()
		} else {
			m.confirmAction = "delete"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Yank):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if t.Meta.ID != "" {
			m.copyToClipboard(t.Meta.ID)
		} else {
			m.toastMsg = "no id set"
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.YankMenu):
		t := m.selectedTask()
		if t == nil {
			break
		}
		m.yankItems = nil
		m.yankCursor = 0
		if t.Meta.Branch != "" {
			m.yankItems = append(m.yankItems, yankItem{"branch", t.Meta.Branch})
		}
		m.yankItems = append(m.yankItems, yankItem{"id", t.Meta.ID})
		m.yankItems = append(m.yankItems, yankItem{"title", t.Meta.Title})
		m.yankItems = append(m.yankItems, yankItem{"path", t.FilePath})
		if sid := lastSession(t); sid != "" {
			m.yankItems = append(m.yankItems, yankItem{"session", sid})
		}
		for name, url := range t.Meta.Links {
			m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
		}
		if len(m.yankItems) > 0 {
			m.yankMenu = true
		}

	case key.Matches(msg, common.Keys.Links):
		t := m.selectedTask()
		if t == nil || len(t.Meta.Links) == 0 {
			break
		}
		m.linkItems = nil
		m.linksCursor = 0
		for name, url := range t.Meta.Links {
			m.linkItems = append(m.linkItems, linkItem{name, url})
		}
		m.linksMenu = true

	case key.Matches(msg, common.Keys.Archive):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if m.confirmAction == "archive" && m.confirmTaskID == t.Meta.ID {
			m.doArchive(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
		} else {
			m.confirmAction = "archive"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.ToggleArchive):
		m.currentView = viewArchive
		m.archiveCursor = 0
		m.confirmAction = ""
		m.confirmTaskID = ""

	case key.Matches(msg, common.Keys.ProjectInfo):
		var slug string
		if m.activeProject == 0 {
			t := m.selectedTask()
			if t == nil {
				break
			}
			slug = t.Project
		} else {
			slug = m.projects[m.activeProject]
		}
		proj, _ := m.store.GetProject(slug)
		if proj != nil {
			m.previousView = viewBoard
			m.currentView = viewProjectInfo
			m.infoProject = proj
			m.infoSlug = slug
			m.detailViewport = viewport.New(m.width, m.height-2)
			m.detailViewport.SetContent(renderProjectInfo(proj, slug, m.width))
		}

	case key.Matches(msg, common.Keys.Undo):
		m.doUndo()

	case key.Matches(msg, common.Keys.ColumnVis):
		var allStatuses []storage.TaskStatus
		if m.activeProject == 0 {
			allStatuses = m.store.GetAllStatuses()
		} else {
			slug := m.projects[m.activeProject]
			allStatuses = m.store.GetProjectStatuses(slug)
		}
		visibleSet := make(map[storage.TaskStatus]bool)
		for _, s := range m.statuses {
			visibleSet[s] = true
		}
		m.colVisItems = nil
		m.colVisCursor = 0
		for _, s := range allStatuses {
			m.colVisItems = append(m.colVisItems, colVisItem{status: s, visible: visibleSet[s]})
		}
		m.colVisMenu = true

	case key.Matches(msg, common.Keys.Select):
		m.selecting = true
		if t := m.selectedTask(); t != nil {
			m.selected[t.Meta.ID] = true
		}

	case key.Matches(msg, common.Keys.ReorderDown):
		m.doReorder(1)

	case key.Matches(msg, common.Keys.ReorderUp):
		m.doReorder(-1)

	case key.Matches(msg, common.Keys.Zoom):
		m.zoomed = !m.zoomed

	case key.Matches(msg, common.Keys.Claude):
		t := m.selectedTask()
		if t == nil {
			break
		}
		m.openClaudeMenu(t)

	case key.Matches(msg, common.Keys.Executor):
		t := m.selectedTask()
		if t == nil {
			break
		}
		m.openExecutorMenu(t)

	case key.Matches(msg, common.Keys.WatchExecutor):
		t := m.selectedTask()
		if t == nil {
			break
		}
		if !m.openExecutorView(t) {
			m.toastMsg = "no executor run for this task (launch with X)"
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case key.Matches(msg, common.Keys.KillRun):
		t := m.selectedTask()
		if t == nil {
			break
		}
		st := m.runForTask(t)
		if st == nil || !st.IsLive() {
			m.toastMsg = "no live executor run to stop"
			m.toastExpiry = time.Now().Add(3 * time.Second)
			break
		}
		if m.confirmAction == "kill-run" && m.confirmTaskID == st.TaskID {
			m.confirmAction = ""
			m.confirmTaskID = ""
			return m, m.killRun(st)
		}
		m.confirmAction = "kill-run"
		m.confirmTaskID = st.TaskID

	case key.Matches(msg, common.Keys.Focus):
		if t := m.selectedTask(); t != nil {
			m.focusPlan.Toggle(t.Meta.ID)
			if m.focusPlan.Contains(t.Meta.ID) {
				m.toastMsg = "Added to focus"
			} else {
				m.toastMsg = "Removed from focus"
			}
			m.focusPlan.Date = storage.Today()
			m.saveFocusPlan()
			m.rebuildFocusSet()
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.FocusView):
		m.currentView = viewFocus
		m.focusCursor = 0

	case key.Matches(msg, common.Keys.Help):
		m.showHelp = true

	case key.Matches(msg, common.Keys.Add):
		if m.activeProject == 0 {
			if len(m.projects) > 1 {
				m.activeProject = 1
				m.reload()
				m.activeCol = 0
			} else {
				return m, nil
			}
		}
		m.adding = true
		m.addStep = 0
		m.addInput.Prompt = "Title: "
		m.addInput.SetValue("")
		m.addInput.Focus()
		return m, m.addInput.Cursor.BlinkCmd()

	case key.Matches(msg, common.Keys.ProjectPicker):
		m.openProjectPicker()

	case key.Matches(msg, common.Keys.Search):
		m.searching = true
		m.searchInput.SetValue(m.searchQuery)
		m.searchInput.Focus()
		return m, m.searchInput.Cursor.BlinkCmd()

	default:
		// numeric shortcuts 1-9: jump to visible project by position
		if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '9' {
			n := int(msg.Runes[0] - '0')
			visible := m.visibleProjects()
			if n <= len(visible) {
				target := visible[n-1]
				for i, p := range m.projects {
					if p == target {
						m.activeProject = i
						m.reload()
						break
					}
				}
			}
		}
	}
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
