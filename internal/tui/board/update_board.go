package board

import (
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/mbalazy/pm-cli/internal/tui/common"
)

// boardKey is one entry of the board's key table: the bindings it answers to
// and what it does. handle mutates the model in place and returns the command
// to run, nil for none.
type boardKey struct {
	keys   []*key.Binding
	handle func(*Model) tea.Cmd
}

func (k boardKey) matches(msg tea.KeyMsg) bool {
	for _, b := range k.keys {
		if key.Matches(msg, *b) {
			return true
		}
	}
	return false
}

// refreshKey is the board's plain "r". Not in common.Keys: there "r" is
// Restore, which only the archive view binds.
var refreshKey = key.NewBinding(key.WithKeys("r"))

// boardKeys is the board view's keyboard, first match wins. The bindings are
// held by pointer so the table reads common.Keys at key time, not at package
// init. A key no entry claims falls through to jumpToProjectKey (1-9).
var boardKeys = []boardKey{
	{[]*key.Binding{&common.Keys.Quit}, (*Model).boardQuit},
	{[]*key.Binding{&common.Keys.Left}, (*Model).boardLeft},
	{[]*key.Binding{&common.Keys.Right}, (*Model).boardRight},
	{[]*key.Binding{&common.Keys.Up}, (*Model).boardUp},
	{[]*key.Binding{&common.Keys.Down}, (*Model).boardDown},
	{[]*key.Binding{&common.Keys.JumpTop}, (*Model).boardJumpTop},
	{[]*key.Binding{&common.Keys.JumpBottom}, (*Model).boardJumpBottom},
	{[]*key.Binding{&common.Keys.HalfDown}, (*Model).boardHalfDown},
	{[]*key.Binding{&common.Keys.HalfUp}, (*Model).boardHalfUp},
	{[]*key.Binding{&refreshKey}, (*Model).boardRefresh},
	{[]*key.Binding{&common.Keys.Tab}, func(m *Model) tea.Cmd { return m.boardSwitchProject(1) }},
	{[]*key.Binding{&common.Keys.ShiftTab}, func(m *Model) tea.Cmd { return m.boardSwitchProject(-1) }},
	{[]*key.Binding{&common.Keys.MoveBack}, withTask((*Model).doMoveBack)},
	{[]*key.Binding{&common.Keys.Move}, withTask((*Model).doMoveForward)},
	{[]*key.Binding{&common.Keys.Enter, &common.Keys.Open, &common.Keys.Space}, withTask((*Model).boardOpenDetail)},
	{[]*key.Binding{&common.Keys.Edit}, onTask(func(m *Model, t *storage.Task) tea.Cmd { return openEditor(t.FilePath) })},
	{[]*key.Binding{&common.Keys.Done}, confirmTwice("done", (*Model).doDone)},
	{[]*key.Binding{&common.Keys.Waiting}, confirmTwice("waiting", (*Model).doWaiting)},
	{[]*key.Binding{&common.Keys.Delete}, confirmTwice("delete", (*Model).boardDelete)},
	{[]*key.Binding{&common.Keys.Yank}, withTask((*Model).boardYank)},
	{[]*key.Binding{&common.Keys.YankMenu}, withTask((*Model).boardYankMenu)},
	{[]*key.Binding{&common.Keys.Links}, withTask((*Model).boardLinksMenu)},
	{[]*key.Binding{&common.Keys.Archive}, confirmTwice("archive", (*Model).doArchive)},
	{[]*key.Binding{&common.Keys.ToggleArchive}, (*Model).boardArchiveView},
	{[]*key.Binding{&common.Keys.ProjectInfo}, (*Model).boardProjectInfo},
	{[]*key.Binding{&common.Keys.Undo}, func(m *Model) tea.Cmd { m.doUndo(); return nil }},
	{[]*key.Binding{&common.Keys.ColumnVis}, (*Model).boardColumnMenu},
	{[]*key.Binding{&common.Keys.Select}, (*Model).boardSelect},
	{[]*key.Binding{&common.Keys.ReorderDown}, func(m *Model) tea.Cmd { m.doReorder(1); return nil }},
	{[]*key.Binding{&common.Keys.ReorderUp}, func(m *Model) tea.Cmd { m.doReorder(-1); return nil }},
	{[]*key.Binding{&common.Keys.Zoom}, func(m *Model) tea.Cmd { m.zoomed = !m.zoomed; return nil }},
	{[]*key.Binding{&common.Keys.Claude}, withTask((*Model).openClaudeMenu)},
	{[]*key.Binding{&common.Keys.Executor}, withTask((*Model).openExecutorMenu)},
	{[]*key.Binding{&common.Keys.WatchExecutor}, withTask((*Model).boardWatchRun)},
	{[]*key.Binding{&common.Keys.KillRun}, onTask((*Model).boardKillRun)},
	{[]*key.Binding{&common.Keys.Focus}, withTask((*Model).boardToggleFocus)},
	{[]*key.Binding{&common.Keys.FocusView}, func(m *Model) tea.Cmd { m.currentView = viewFocus; m.focusCursor = 0; return nil }},
	{[]*key.Binding{&common.Keys.RunsView}, func(m *Model) tea.Cmd { m.openRunsView(); return nil }},
	{[]*key.Binding{&common.Keys.Help}, func(m *Model) tea.Cmd { m.showHelp = true; return nil }},
	{[]*key.Binding{&common.Keys.Add}, (*Model).boardAdd},
	{[]*key.Binding{&common.Keys.ProjectPicker}, func(m *Model) tea.Cmd { m.openProjectPicker(); return nil }},
	{[]*key.Binding{&common.Keys.Search}, (*Model).boardSearch},
}

// onTask adapts a handler of the selected task to a board key: with no task
// under the cursor the key does nothing.
func onTask(f func(*Model, *storage.Task) tea.Cmd) func(*Model) tea.Cmd {
	return func(m *Model) tea.Cmd {
		t := m.selectedTask()
		if t == nil {
			return nil
		}
		return f(m, t)
	}
}

// withTask is onTask for a handler that returns no command.
func withTask(f func(*Model, *storage.Task)) func(*Model) tea.Cmd {
	return onTask(func(m *Model, t *storage.Task) tea.Cmd {
		f(m, t)
		return nil
	})
}

// confirmTwice is the confirmation pattern of the destructive task keys: the
// first press arms action for the selected task, a second press on the same
// task runs do.
func confirmTwice(action string, do func(*Model, *storage.Task)) func(*Model) tea.Cmd {
	return withTask(func(m *Model, t *storage.Task) {
		if m.confirmAction == action && m.confirmTaskID == t.Meta.ID {
			do(m, t)
			m.confirmAction = ""
			m.confirmTaskID = ""
		} else {
			m.confirmAction = action
			m.confirmTaskID = t.Meta.ID
		}
	})
}

// confirmKeys maps an armed confirmation to the key that confirms it.
var confirmKeys = map[string]*key.Binding{
	"done":     &common.Keys.Done,
	"waiting":  &common.Keys.Waiting,
	"delete":   &common.Keys.Delete,
	"archive":  &common.Keys.Archive,
	"quit":     &common.Keys.Quit,
	"kill-run": &common.Keys.KillRun,
}

// dropStaleConfirm is the other half of the confirmation pattern: any key other
// than the confirming one disarms it, and is then handled as usual.
func (m *Model) dropStaleConfirm(msg tea.KeyMsg) {
	if m.confirmAction == "" {
		return
	}
	if b, ok := confirmKeys[m.confirmAction]; ok && key.Matches(msg, *b) {
		return
	}
	m.confirmAction = ""
	m.confirmTaskID = ""
}

func (m *Model) boardQuit() tea.Cmd {
	if m.confirmAction == "quit" {
		return tea.Quit
	}
	m.confirmAction = "quit"
	return nil
}

func (m *Model) boardLeft() tea.Cmd {
	if len(m.statuses) > 0 {
		m.activeCol = (m.activeCol - 1 + len(m.statuses)) % len(m.statuses)
	}
	return nil
}

func (m *Model) boardRight() tea.Cmd {
	if len(m.statuses) > 0 {
		m.activeCol = (m.activeCol + 1) % len(m.statuses)
	}
	return nil
}

func (m *Model) boardUp() tea.Cmd {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) > 0 && m.cursors[m.activeCol] > 0 {
		m.cursors[m.activeCol]--
		m.fixScrollOffsets()
	}
	return nil
}

func (m *Model) boardDown() tea.Cmd {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) > 0 && m.cursors[m.activeCol] < len(tasks)-1 {
		m.cursors[m.activeCol]++
		m.fixScrollOffsets()
	}
	return nil
}

func (m *Model) boardJumpTop() tea.Cmd {
	// With every column hidden m.cursors is empty and m.activeCol is 0 -
	// unguarded indexing here panicked the whole board.
	if len(m.statuses) > 0 {
		m.cursors[m.activeCol] = 0
		m.fixScrollOffsets()
	}
	return nil
}

func (m *Model) boardJumpBottom() tea.Cmd {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) > 0 {
		m.cursors[m.activeCol] = len(tasks) - 1
		m.fixScrollOffsets()
	}
	return nil
}

func (m *Model) boardHalfDown() tea.Cmd {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) > 0 {
		m.cursors[m.activeCol] = min(m.cursors[m.activeCol]+5, len(tasks)-1)
		m.fixScrollOffsets()
	}
	return nil
}

func (m *Model) boardHalfUp() tea.Cmd {
	if len(m.statuses) > 0 {
		m.cursors[m.activeCol] = max(m.cursors[m.activeCol]-5, 0)
		m.fixScrollOffsets()
	}
	return nil
}

func (m *Model) boardRefresh() tea.Cmd {
	m.refreshProjects()
	m.reload()
	m.toastMsg = "Refreshed"
	m.toastExpiry = time.Now().Add(2 * time.Second)
	return nil
}

func (m *Model) boardSwitchProject(dir int) tea.Cmd {
	m.activeProject = m.nextVisibleProject(dir)
	m.reload()
	return nil
}

func (m *Model) boardOpenDetail(t *storage.Task) {
	m.previousView = viewBoard
	m.currentView = viewDetail
	m.openDetailTask(t)
}

func (m *Model) boardDelete(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "delete", task: snapshotTask(t)}
	if err := m.store.DeleteTask(t); err != nil {
		m.showErrorToast("delete failed", err)
	}
	m.reload()
}

func (m *Model) boardYank(t *storage.Task) {
	if t.Meta.ID != "" {
		m.copyToClipboard(t.Meta.ID)
	} else {
		m.toastMsg = "no id set"
		m.toastExpiry = time.Now().Add(2 * time.Second)
	}
}

func (m *Model) boardYankMenu(t *storage.Task) {
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
}

func (m *Model) boardLinksMenu(t *storage.Task) {
	if len(t.Meta.Links) == 0 {
		return
	}
	m.linkItems = nil
	m.linksCursor = 0
	for name, url := range t.Meta.Links {
		m.linkItems = append(m.linkItems, linkItem{name, url})
	}
	m.linksMenu = true
}

func (m *Model) boardArchiveView() tea.Cmd {
	m.currentView = viewArchive
	m.archiveCursor = 0
	m.confirmAction = ""
	m.confirmTaskID = ""
	return nil
}

func (m *Model) boardProjectInfo() tea.Cmd {
	var slug string
	if m.activeProject == 0 {
		t := m.selectedTask()
		if t == nil {
			return nil
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
	return nil
}

func (m *Model) boardColumnMenu() tea.Cmd {
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
	return nil
}

func (m *Model) boardSelect() tea.Cmd {
	m.selecting = true
	if t := m.selectedTask(); t != nil {
		m.selected[t.Meta.ID] = true
	}
	return nil
}

func (m *Model) boardWatchRun(t *storage.Task) {
	if !m.openExecutorView(t) {
		m.toastMsg = "no executor run for this task (launch with x)"
		m.toastExpiry = time.Now().Add(3 * time.Second)
	}
}

func (m *Model) boardKillRun(t *storage.Task) tea.Cmd {
	st, finish := m.pickRunForTask(t)
	if st == nil || !st.IsLive() {
		m.toastMsg = "no live executor run to stop"
		m.toastExpiry = time.Now().Add(3 * time.Second)
		return nil
	}
	// The confirmation is keyed on the run AND its kind: the two share a
	// task id, so a run that ends between the two presses would otherwise
	// turn a confirmation given for it into a kill of the acceptance.
	if m.confirmAction == "kill-run" && m.confirmTaskID == st.TaskID && m.confirmRunFinish == finish {
		m.confirmAction = ""
		m.confirmTaskID = ""
		return m.killRun(st)
	}
	m.confirmAction = "kill-run"
	m.confirmTaskID = st.TaskID
	m.confirmRunFinish = finish
	return nil
}

func (m *Model) boardToggleFocus(t *storage.Task) {
	m.focusPlan.Toggle(t.Meta.ID)
	added := m.focusPlan.Contains(t.Meta.ID)
	m.focusPlan.Date = storage.Today()
	// Toast set only on a successful save - otherwise the unconditional
	// "Added/Removed" text + 2s expiry clobbers the error toast
	// saveFocusPlan just set (message and its 15s expiry both).
	if m.saveFocusPlan() {
		if added {
			m.toastMsg = "Added to focus"
		} else {
			m.toastMsg = "Removed from focus"
		}
		m.toastExpiry = time.Now().Add(2 * time.Second)
	}
	m.rebuildFocusSet()
}

func (m *Model) boardAdd() tea.Cmd {
	if m.activeProject == 0 {
		if len(m.projects) > 1 {
			m.activeProject = 1
			m.reload()
			m.activeCol = 0
		} else {
			return nil
		}
	}
	// All columns can be hidden via the V menu (applyColumnVisibility) -
	// there is then no status to create the task into.
	if len(m.statuses) == 0 {
		m.toastMsg = "no visible columns to add to"
		m.toastExpiry = time.Now().Add(3 * time.Second)
		return nil
	}
	m.adding = true
	m.addStep = 0
	m.addInput.Prompt = "Title: "
	m.addInput.SetValue("")
	m.addInput.Focus()
	return m.addInput.Cursor.BlinkCmd()
}

func (m *Model) boardSearch() tea.Cmd {
	m.searching = true
	m.searchInput.SetValue(m.searchQuery)
	m.searchInput.Focus()
	return m.searchInput.Cursor.BlinkCmd()
}

// jumpToProjectKey handles the numeric shortcuts 1-9: jump to the visible
// project at that position.
func (m *Model) jumpToProjectKey(msg tea.KeyMsg) {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 || msg.Runes[0] < '1' || msg.Runes[0] > '9' {
		return
	}
	n := int(msg.Runes[0] - '0')
	visible := m.visibleProjects()
	if n > len(visible) {
		return
	}
	target := visible[n-1]
	for i, p := range m.projects {
		if p == target {
			m.activeProject = i
			m.reload()
			break
		}
	}
}
