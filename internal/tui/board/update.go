package board

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
		if m.currentView == viewExecutor {
			m.refreshExecutorView()
		}
		return m, doTick()

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
		if m.currentView == viewExecutor {
			return m.updateExecutorView(msg)
		}
		if m.currentView == viewDetail || m.currentView == viewProjectInfo {
			return m.updateDetail(msg)
		}
		if m.projectPicker {
			return m.updateProjectPicker(msg)
		}
		if m.colVisMenu {
			return m.updateColVisMenu(msg)
		}
		if m.yankMenu {
			return m.updateYankMenu(msg)
		}
		if m.linksMenu {
			return m.updateLinksMenu(msg)
		}
		if m.claudeMenu {
			return m.updateClaudeMenu(msg)
		}
		if m.currentView == viewArchive {
			return m.updateArchive(msg)
		}
		if m.currentView == viewFocus {
			return m.updateFocus(msg)
		}
		if m.showHelp {
			if m.helpSearch {
				return m.updateHelpSearch(msg)
			}
			switch {
			case key.Matches(msg, common.Keys.Search):
				m.helpSearch = true
				m.helpInput.SetValue(m.helpFilter)
				m.helpInput.Focus()
				return m, m.helpInput.Cursor.BlinkCmd()
			case key.Matches(msg, common.Keys.Escape):
				if m.helpFilter != "" {
					m.helpFilter = ""
					return m, nil
				}
				m.showHelp = false
				return m, nil
			default:
				m.showHelp = false
				m.helpFilter = ""
				return m, nil
			}
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
		m.cursors[m.activeCol] = 0
		m.fixScrollOffsets()

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
		m.cursors[m.activeCol] = max(m.cursors[m.activeCol]-5, 0)
		m.fixScrollOffsets()

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

func (m *Model) openProjectPicker() {
	m.pickerItems = nil
	m.pickerCursor = 0
	m.pickerFilter = ""
	m.pickerInput.SetValue("")
	m.pickerInput.Blur()

	allTasks, _ := m.store.GetAllTasks()

	// aggregate per-project stats
	type projectStats struct {
		statusCounts map[storage.TaskStatus]int
		lastUpdated  string
	}
	stats := make(map[string]*projectStats)
	for _, t := range allTasks {
		if t.Meta.Status == storage.StatusArchived {
			continue
		}
		s, ok := stats[t.Project]
		if !ok {
			s = &projectStats{statusCounts: make(map[storage.TaskStatus]int)}
			stats[t.Project] = s
		}
		s.statusCounts[t.Meta.Status]++
		if t.Meta.Updated > s.lastUpdated {
			s.lastUpdated = t.Meta.Updated
		}
	}

	for _, slug := range m.projects[1:] { // skip "all"
		proj, _ := m.store.GetProject(slug)
		name := slug
		stack := ""
		var path, repo string
		var links map[string]string
		var tags []string
		if proj != nil {
			if proj.Name != "" {
				name = proj.Name
			}
			stack = proj.Stack
			path = proj.Path
			repo = proj.Repo
			links = proj.Links
			tags = proj.Tags
		}
		item := pickerItem{
			slug:     slug,
			name:     name,
			stack:    stack,
			hidden:   m.hiddenProjects[slug],
			path:     path,
			repo:     repo,
			links:    links,
			tags:     tags,
			statuses: m.store.GetProjectStatuses(slug),
		}
		if s, ok := stats[slug]; ok {
			item.statusCounts = s.statusCounts
			item.lastUpdated = s.lastUpdated
			total := 0
			for _, c := range s.statusCounts {
				total += c
			}
			item.taskCount = total
			item.doingCount = s.statusCounts[storage.ParseStatus("doing")]
		}
		m.pickerItems = append(m.pickerItems, item)
	}

	m.sortPickerItems()
	m.projectPicker = true
}

func (m *Model) sortPickerItems() {
	sort.SliceStable(m.pickerItems, func(i, j int) bool {
		a, b := m.pickerItems[i], m.pickerItems[j]
		// only rule: hidden projects sink to the bottom
		if a.hidden != b.hidden {
			return !a.hidden
		}
		return false // preserve insertion order (from m.projects)
	})
}

func (m Model) filteredPickerItems() []pickerItem {
	if m.pickerFilter == "" {
		return m.pickerItems
	}
	q := strings.ToLower(m.pickerFilter)
	var result []pickerItem
	for _, item := range m.pickerItems {
		if strings.Contains(strings.ToLower(item.name), q) ||
			strings.Contains(strings.ToLower(item.slug), q) ||
			strings.Contains(strings.ToLower(item.stack), q) {
			result = append(result, item)
		}
	}
	return result
}

func (m *Model) saveTUIState() {
	var hidden []string
	for slug, h := range m.hiddenProjects {
		if h {
			hidden = append(hidden, slug)
		}
	}
	sort.Strings(hidden)
	saveTUIConfig(m.store.RootDir(), tuiConfig{
		HiddenProjects: hidden,
		ProjectOrder:   m.projects[1:], // skip "all"
	})
}

func (m Model) updateProjectPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.yankMenu {
		return m.updateYankMenu(msg)
	}
	// handle filter input mode
	if m.pickerInput.Focused() {
		switch {
		case key.Matches(msg, common.Keys.Enter):
			m.pickerFilter = m.pickerInput.Value()
			m.pickerInput.Blur()
			m.pickerCursor = 0
			return m, nil
		case key.Matches(msg, common.Keys.Escape):
			if m.pickerFilter != "" || m.pickerInput.Value() != "" {
				m.pickerFilter = ""
				m.pickerInput.SetValue("")
				m.pickerInput.Blur()
				m.pickerCursor = 0
				return m, nil
			}
			m.pickerInput.Blur()
			m.projectPicker = false
			return m, nil
		default:
			var cmd tea.Cmd
			m.pickerInput, cmd = m.pickerInput.Update(msg)
			m.pickerFilter = m.pickerInput.Value()
			m.pickerCursor = 0
			return m, cmd
		}
	}

	items := m.filteredPickerItems()

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit):
		if m.pickerFilter != "" {
			m.pickerFilter = ""
			m.pickerInput.SetValue("")
			m.pickerCursor = 0
			return m, nil
		}
		m.projectPicker = false

	case key.Matches(msg, common.Keys.Up):
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}

	case key.Matches(msg, common.Keys.Down):
		if m.pickerCursor < len(items)-1 {
			m.pickerCursor++
		}

	case key.Matches(msg, common.Keys.Enter):
		if m.pickerCursor < len(items) {
			slug := items[m.pickerCursor].slug
			for i, p := range m.projects {
				if p == slug {
					m.activeProject = i
					break
				}
			}
			m.projectPicker = false
			m.pickerFilter = ""
			m.reload()
		}

	case key.Matches(msg, common.Keys.Space):
		if m.pickerCursor < len(items) {
			slug := items[m.pickerCursor].slug
			if m.hiddenProjects[slug] {
				delete(m.hiddenProjects, slug)
			} else {
				m.hiddenProjects[slug] = true
			}
			for i := range m.pickerItems {
				if m.pickerItems[i].slug == slug {
					m.pickerItems[i].hidden = m.hiddenProjects[slug]
				}
			}
			m.saveTUIState()
			m.sortPickerItems()
			filtered := m.filteredPickerItems()
			if m.pickerCursor >= len(filtered) {
				m.pickerCursor = max(0, len(filtered)-1)
			}
		}

	case key.Matches(msg, common.Keys.ReorderDown):
		if m.pickerFilter == "" && m.pickerCursor < len(m.pickerItems)-1 {
			m.pickerReorder(1)
			m.saveTUIState()
		}

	case key.Matches(msg, common.Keys.ReorderUp):
		if m.pickerFilter == "" && m.pickerCursor > 0 {
			m.pickerReorder(-1)
			m.saveTUIState()
		}

	case key.Matches(msg, common.Keys.Claude):
		if m.pickerCursor < len(items) {
			slug := items[m.pickerCursor].slug
			m.projectPicker = false
			m.pickerFilter = ""
			m.openProjectClaudeMenu(slug)
		}

	case key.Matches(msg, common.Keys.ProjectInfo):
		if m.pickerCursor < len(items) {
			slug := items[m.pickerCursor].slug
			proj, _ := m.store.GetProject(slug)
			if proj != nil {
				m.projectPicker = false
				m.pickerFilter = ""
				m.previousView = viewBoard
				m.currentView = viewProjectInfo
				m.infoProject = proj
				m.infoSlug = slug
				m.detailViewport = viewport.New(m.width, m.height-2)
				m.detailViewport.SetContent(renderProjectInfo(proj, slug, m.width))
			}
		}

	case key.Matches(msg, common.Keys.Yank):
		if m.pickerCursor < len(items) {
			item := items[m.pickerCursor]
			if item.path != "" {
				m.copyToClipboard(item.path)
			} else {
				m.copyToClipboard(item.slug)
			}
		}

	case key.Matches(msg, common.Keys.YankMenu):
		if m.pickerCursor < len(items) {
			item := items[m.pickerCursor]
			m.yankItems = nil
			m.yankCursor = 0
			m.yankItems = append(m.yankItems, yankItem{"slug", item.slug})
			m.yankItems = append(m.yankItems, yankItem{"name", item.name})
			if item.path != "" {
				m.yankItems = append(m.yankItems, yankItem{"path", item.path})
			}
			if item.repo != "" {
				m.yankItems = append(m.yankItems, yankItem{"repo", item.repo})
			}
			if item.stack != "" {
				m.yankItems = append(m.yankItems, yankItem{"stack", item.stack})
			}
			for name, url := range item.links {
				m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
			}
			if len(item.tags) > 0 {
				m.yankItems = append(m.yankItems, yankItem{"tags", strings.Join(item.tags, ", ")})
			}
			if len(m.yankItems) > 0 {
				m.yankMenu = true
			}
		}

	case key.Matches(msg, common.Keys.Search):
		m.pickerInput.SetValue(m.pickerFilter)
		m.pickerInput.Focus()
		return m, m.pickerInput.Cursor.BlinkCmd()
	}

	return m, nil
}

func (m *Model) pickerReorder(dir int) {
	target := m.pickerCursor + dir
	if target < 0 || target >= len(m.pickerItems) {
		return
	}
	// don't swap across hidden/visible boundary
	if m.pickerItems[m.pickerCursor].hidden != m.pickerItems[target].hidden {
		return
	}
	// swap in picker
	m.pickerItems[m.pickerCursor], m.pickerItems[target] = m.pickerItems[target], m.pickerItems[m.pickerCursor]
	m.pickerCursor = target

	// rebuild m.projects from picker order (visible first, then hidden - matching picker)
	newProjects := []string{"all"}
	for _, item := range m.pickerItems {
		newProjects = append(newProjects, item.slug)
	}
	// preserve activeProject by slug
	activeSlug := ""
	if m.activeProject > 0 && m.activeProject < len(m.projects) {
		activeSlug = m.projects[m.activeProject]
	}
	m.projects = newProjects
	if activeSlug != "" {
		for i, p := range m.projects {
			if p == activeSlug {
				m.activeProject = i
				break
			}
		}
	}
}

func (m Model) updateSelectMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Confirmation for bulk delete
	if m.confirmAction == "delete-selected" {
		if key.Matches(msg, common.Keys.Delete) {
			tasks := m.markedTasks()
			count := 0
			for _, t := range tasks {
				m.store.DeleteTask(t)
				count++
			}
			m.confirmAction = ""
			m.selecting = false
			m.selected = make(map[string]bool)
			m.reload()
			m.toastMsg = fmt.Sprintf("Deleted %d tasks", count)
			m.toastExpiry = time.Now().Add(2 * time.Second)
			return m, nil
		}
		m.confirmAction = ""
		return m, nil
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit):
		m.selecting = false
		m.selected = make(map[string]bool)

	// Navigation - same as board
	case key.Matches(msg, common.Keys.Up):
		if m.cursors[m.activeCol] > 0 {
			m.cursors[m.activeCol]--
			m.fixScrollOffsets()
		}
	case key.Matches(msg, common.Keys.Down):
		tasks := m.columnTasks(m.activeCol)
		if m.cursors[m.activeCol] < len(tasks)-1 {
			m.cursors[m.activeCol]++
			m.fixScrollOffsets()
		}
	case key.Matches(msg, common.Keys.Left):
		if m.activeCol > 0 {
			m.activeCol--
		}
	case key.Matches(msg, common.Keys.Right):
		if m.activeCol < len(m.statuses)-1 {
			m.activeCol++
		}
	case key.Matches(msg, common.Keys.JumpTop):
		m.cursors[m.activeCol] = 0
		m.fixScrollOffsets()
	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = len(tasks) - 1
			m.fixScrollOffsets()
		}

	// Toggle selection
	case key.Matches(msg, common.Keys.Space), key.Matches(msg, common.Keys.Select):
		if t := m.selectedTask(); t != nil {
			if m.selected[t.Meta.ID] {
				delete(m.selected, t.Meta.ID)
			} else {
				m.selected[t.Meta.ID] = true
			}
		}

	// Bulk actions
	case key.Matches(msg, common.Keys.Move):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			for _, t := range tasks {
				idx := m.statusIndex(t.Meta.Status)
				m.store.MoveTask(t, m.statuses[(idx+1)%len(m.statuses)])
			}
			m.selecting = false
			m.selected = make(map[string]bool)
			m.reload()
			m.toastMsg = fmt.Sprintf("Moved %d tasks forward", len(tasks))
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.MoveBack):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			for _, t := range tasks {
				idx := m.statusIndex(t.Meta.Status)
				var newStatus storage.TaskStatus
				if idx > 0 {
					newStatus = m.statuses[idx-1]
				} else {
					newStatus = m.statuses[len(m.statuses)-1]
				}
				m.store.MoveTask(t, newStatus)
			}
			m.selecting = false
			m.selected = make(map[string]bool)
			m.reload()
			m.toastMsg = fmt.Sprintf("Moved %d tasks back", len(tasks))
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.Done):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			for _, t := range tasks {
				m.store.MoveTask(t, m.statuses[len(m.statuses)-1])
			}
			m.selecting = false
			m.selected = make(map[string]bool)
			m.reload()
			m.toastMsg = fmt.Sprintf("Marked %d tasks done", len(tasks))
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.Waiting):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			for _, t := range tasks {
				m.store.MoveTask(t, storage.StatusWaiting)
			}
			m.selecting = false
			m.selected = make(map[string]bool)
			m.reload()
			m.toastMsg = fmt.Sprintf("Marked %d tasks waiting", len(tasks))
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.Archive):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			for _, t := range tasks {
				m.store.MoveTask(t, storage.StatusArchived)
			}
			m.selecting = false
			m.selected = make(map[string]bool)
			m.reload()
			m.toastMsg = fmt.Sprintf("Archived %d tasks", len(tasks))
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.Delete):
		if len(m.selected) > 0 {
			m.confirmAction = "delete-selected"
		}

	case key.Matches(msg, common.Keys.Yank):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			var ids []string
			for _, t := range tasks {
				ids = append(ids, t.Meta.ID)
			}
			m.copyToClipboard(strings.Join(ids, "\n"))
		}

	case key.Matches(msg, common.Keys.YankMenu):
		tasks := m.markedTasks()
		if len(tasks) > 0 {
			var ids, titles, idTitles, branches, paths []string
			for _, t := range tasks {
				ids = append(ids, t.Meta.ID)
				titles = append(titles, t.Meta.Title)
				idTitles = append(idTitles, t.Meta.ID+": "+t.Meta.Title)
				if t.Meta.Branch != "" {
					branches = append(branches, t.Meta.Branch)
				}
				paths = append(paths, t.FilePath)
			}
			m.yankItems = nil
			m.yankCursor = 0
			m.yankItems = append(m.yankItems, yankItem{"IDs", strings.Join(ids, "\n")})
			m.yankItems = append(m.yankItems, yankItem{"titles", strings.Join(titles, "\n")})
			m.yankItems = append(m.yankItems, yankItem{"IDs + titles", strings.Join(idTitles, "\n")})
			if len(branches) > 0 {
				m.yankItems = append(m.yankItems, yankItem{"branches", strings.Join(branches, "\n")})
			}
			m.yankItems = append(m.yankItems, yankItem{"file paths", strings.Join(paths, "\n")})
			m.yankMenu = true
		}
	}

	return m, nil
}

func (m Model) updateHelpSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Enter):
		m.helpSearch = false
		m.helpFilter = m.helpInput.Value()
		m.helpInput.Blur()
	case key.Matches(msg, common.Keys.Escape):
		m.helpSearch = false
		m.helpFilter = ""
		m.helpInput.SetValue("")
		m.helpInput.Blur()
	default:
		var cmd tea.Cmd
		m.helpInput, cmd = m.helpInput.Update(msg)
		m.helpFilter = m.helpInput.Value()
		return m, cmd
	}
	return m, nil
}

func (m Model) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape):
		m.adding = false
		m.addInput.Blur()
	case key.Matches(msg, common.Keys.Enter):
		val := strings.TrimSpace(m.addInput.Value())
		if m.addStep == 0 {
			if val == "" {
				return m, nil
			}
			m.addTitle = val
			m.addStep = 1
			m.addInput.Prompt = "ID (enter for auto): "
			m.addInput.SetValue("")
			return m, nil
		}
		// step 1: create task
		id := val
		if id == "" {
			id = fmt.Sprintf("%d", time.Now().Unix()%100000)
		}
		now := storage.Today()
		t := &storage.Task{
			Meta: storage.TaskMeta{
				ID:      id,
				Title:   m.addTitle,
				Status:  m.statuses[0],
				Created: now,
				Updated: now,
			},
		}
		slug := m.projects[m.activeProject]
		m.store.AddTask(slug, t)
		m.adding = false
		m.addInput.Blur()
		m.reload()
	default:
		var cmd tea.Cmd
		m.addInput, cmd = m.addInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Overlay menus intercept input first
	if m.subtaskPicker {
		return m.updateSubtaskPicker(msg)
	}
	if m.claudeMenu {
		return m.updateClaudeMenu(msg)
	}
	if m.yankMenu {
		return m.updateYankMenu(msg)
	}
	if m.linksMenu {
		return m.updateLinksMenu(msg)
	}
	if m.sessionMenu {
		return m.updateSessionMenu(msg)
	}

	// Detail search input mode
	if m.detailSearching {
		return m.updateDetailSearch(msg)
	}

	// Project info view: close, scroll, yank, links
	if m.currentView == viewProjectInfo {
		switch {
		case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
			m.currentView = m.previousView
			m.reload()
			return m, nil
		case key.Matches(msg, common.Keys.Yank):
			if m.infoSlug != "" {
				m.copyToClipboard(m.infoSlug)
			}
			return m, nil
		case key.Matches(msg, common.Keys.YankMenu):
			if m.infoProject != nil {
				m.yankItems = nil
				m.yankCursor = 0
				m.yankItems = append(m.yankItems, yankItem{"slug", m.infoSlug})
				if m.infoProject.Path != "" {
					m.yankItems = append(m.yankItems, yankItem{"path", m.infoProject.Path})
				}
				if m.infoProject.Repo != "" {
					m.yankItems = append(m.yankItems, yankItem{"repo", m.infoProject.Repo})
				}
				for name, url := range m.infoProject.Links {
					m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
				}
				if len(m.yankItems) > 0 {
					m.yankMenu = true
				}
			}
			return m, nil
		case key.Matches(msg, common.Keys.Links):
			if m.infoProject != nil && len(m.infoProject.Links) > 0 {
				m.linkItems = nil
				m.linksCursor = 0
				for name, url := range m.infoProject.Links {
					m.linkItems = append(m.linkItems, linkItem{name, url})
				}
				m.linksMenu = true
			}
			return m, nil
		case key.Matches(msg, common.Keys.Claude):
			if m.infoSlug != "" {
				m.openProjectClaudeMenu(m.infoSlug)
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.detailViewport, cmd = m.detailViewport.Update(msg)
		return m, cmd
	}

	t := m.detailTask
	switch {
	case key.Matches(msg, common.Keys.Escape):
		if m.detailSearchQuery != "" {
			m.detailSearchQuery = ""
			m.detailSearchMatches = nil
			// Restore original content (remove highlights)
			if t != nil {
				content := m.renderTaskDetail(t)
				m.detailViewport.SetContent(content)
			}
			return m, nil
		}
		m.currentView = m.previousView
		m.reload()
		return m, nil

	case key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		m.currentView = m.previousView
		m.reload()
		return m, nil

	case key.Matches(msg, common.Keys.Search):
		m.detailSearching = true
		m.detailSearchInput.SetValue(m.detailSearchQuery)
		m.detailSearchInput.Focus()
		return m, m.detailSearchInput.Cursor.BlinkCmd()

	case msg.String() == "n":
		if len(m.detailSearchMatches) > 0 {
			m.detailSearchIdx = (m.detailSearchIdx + 1) % len(m.detailSearchMatches)
			m.applySearchHighlights()
			m.detailViewport.SetYOffset(m.detailSearchMatches[m.detailSearchIdx])
		}
		return m, nil

	case msg.String() == "N":
		if len(m.detailSearchMatches) > 0 {
			m.detailSearchIdx = (m.detailSearchIdx - 1 + len(m.detailSearchMatches)) % len(m.detailSearchMatches)
			m.applySearchHighlights()
			m.detailViewport.SetYOffset(m.detailSearchMatches[m.detailSearchIdx])
		}
		return m, nil

	case t != nil && msg.String() == "p":
		// context-aware relation jump: subtask -> parent; tracker -> subtask picker
		if parent := m.taskParent(t); parent != nil {
			m.openDetailTask(parent)
		} else if kids := m.taskChildren(t); len(kids) > 0 {
			m.openSubtaskPicker(kids)
		}
		return m, nil

	case key.Matches(msg, common.Keys.Yank):
		if t != nil && t.Meta.ID != "" {
			m.copyToClipboard(t.Meta.ID)
		}

	case key.Matches(msg, common.Keys.YankMenu):
		if t != nil {
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
			if t.Meta.Brief != "" {
				m.yankItems = append(m.yankItems, yankItem{"brief", t.Meta.Brief})
			}
			if t.Meta.AC != "" {
				m.yankItems = append(m.yankItems, yankItem{"ac", t.Meta.AC})
			}
			if t.Body != "" {
				m.yankItems = append(m.yankItems, yankItem{"body", t.Body})
			}
			for name, url := range t.Meta.Links {
				m.yankItems = append(m.yankItems, yankItem{"link: " + name, url})
			}
			if len(m.yankItems) > 0 {
				m.yankMenu = true
			}
		}

	case key.Matches(msg, common.Keys.Edit):
		if t != nil {
			return m, openEditor(t.FilePath)
		}

	case key.Matches(msg, common.Keys.Links):
		if t != nil && len(t.Meta.Links) > 0 {
			m.linkItems = nil
			m.linksCursor = 0
			for name, url := range t.Meta.Links {
				m.linkItems = append(m.linkItems, linkItem{name, url})
			}
			m.linksMenu = true
		}

	case key.Matches(msg, common.Keys.Move):
		if t != nil {
			m.doMoveForward(t)
			m.currentView = m.previousView
			return m, nil
		}

	case key.Matches(msg, common.Keys.MoveBack):
		if t != nil {
			m.doMoveBack(t)
			m.currentView = m.previousView
			return m, nil
		}

	case key.Matches(msg, common.Keys.Done):
		if t != nil {
			if m.confirmAction == "done" && m.confirmTaskID == t.Meta.ID {
				m.doDone(t)
				m.confirmAction = ""
				m.confirmTaskID = ""
				m.currentView = m.previousView
				return m, nil
			}
			m.confirmAction = "done"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Waiting):
		if t != nil {
			if m.confirmAction == "waiting" && m.confirmTaskID == t.Meta.ID {
				m.doWaiting(t)
				m.confirmAction = ""
				m.confirmTaskID = ""
				m.currentView = m.previousView
				return m, nil
			}
			m.confirmAction = "waiting"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Archive):
		if t != nil {
			if m.confirmAction == "archive" && m.confirmTaskID == t.Meta.ID {
				m.doArchive(t)
				m.confirmAction = ""
				m.confirmTaskID = ""
				m.currentView = m.previousView
				return m, nil
			}
			m.confirmAction = "archive"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Claude):
		if t != nil {
			m.openClaudeMenu(t)
		}

	case key.Matches(msg, common.Keys.Executor):
		if t != nil {
			m.openExecutorMenu(t)
		}

	case key.Matches(msg, common.Keys.WatchExecutor):
		if t != nil && !m.openExecutorView(t) {
			m.toastMsg = "no executor run for this task (launch with X)"
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case msg.String() == "s":
		if t != nil {
			m.openSessionMenu(t)
		}

	case msg.String() == "r":
		if t != nil {
			// reload the whole task set first so the subtask table (rendered from
			// m.tasks via taskChildren) reflects fresh child statuses, not just the
			// parent's own fields.
			m.reload()
			reloaded, err := m.store.FindTask(t.Project, t.Meta.ID)
			if err == nil {
				off := m.detailViewport.YOffset
				m.detailTask = reloaded
				content := m.renderTaskDetail(reloaded)
				m.detailViewport.SetContent(content)
				m.detailPlainContent = stripANSI(content)
				m.detailViewport.SetYOffset(off)
				m.toastMsg = "Refreshed"
				m.toastExpiry = time.Now().Add(2 * time.Second)
			}
		}

	default:
		var cmd tea.Cmd
		m.detailViewport, cmd = m.detailViewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateDetailSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Enter):
		m.detailSearching = false
		m.detailSearchQuery = m.detailSearchInput.Value()
		m.detailSearchInput.Blur()
		m.computeDetailSearchMatches()
		if len(m.detailSearchMatches) > 0 {
			m.detailSearchIdx = 0
			m.detailViewport.SetYOffset(m.detailSearchMatches[0])
		}
	case key.Matches(msg, common.Keys.Escape):
		m.detailSearching = false
		m.detailSearchQuery = ""
		m.detailSearchMatches = nil
		m.detailSearchInput.SetValue("")
		m.detailSearchInput.Blur()
		// Restore original content (remove highlights)
		if m.detailTask != nil {
			content := m.renderTaskDetail(m.detailTask)
			m.detailViewport.SetContent(content)
		}
	default:
		var cmd tea.Cmd
		m.detailSearchInput, cmd = m.detailSearchInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) computeDetailSearchMatches() {
	m.detailSearchMatches = nil
	m.detailSearchIdx = 0
	q := strings.ToLower(m.detailSearchQuery)
	if q == "" {
		// Restore original content (remove highlights)
		if m.detailTask != nil {
			content := m.renderTaskDetail(m.detailTask)
			m.detailViewport.SetContent(content)
		}
		return
	}
	plainLines := strings.Split(m.detailPlainContent, "\n")
	for i, line := range plainLines {
		if strings.Contains(strings.ToLower(line), q) {
			m.detailSearchMatches = append(m.detailSearchMatches, i)
		}
	}

	// Apply highlighting to rendered content
	m.applySearchHighlights()
}

// applySearchHighlights re-renders the viewport content with search highlights.
// The active match line gets a distinct color (cyan), others get yellow.
func (m *Model) applySearchHighlights() {
	if m.detailTask == nil || m.detailSearchQuery == "" {
		return
	}
	original := m.renderTaskDetail(m.detailTask)
	renderedLines := strings.Split(original, "\n")
	q := strings.ToLower(m.detailSearchQuery)

	// Determine which line is the active match
	activeLine := -1
	if len(m.detailSearchMatches) > 0 && m.detailSearchIdx < len(m.detailSearchMatches) {
		activeLine = m.detailSearchMatches[m.detailSearchIdx]
	}

	var highlighted []string
	for i, line := range renderedLines {
		highlighted = append(highlighted, highlightSearchTerm(line, q, i == activeLine))
	}
	m.detailViewport.SetContent(strings.Join(highlighted, "\n"))
}

// highlightSearchTerm highlights occurrences of query in a line that may contain ANSI codes.
// active=true uses cyan highlight for the current match, false uses yellow for others.
func highlightSearchTerm(line, query string, active bool) string {
	plain := stripANSI(line)
	lowerPlain := strings.ToLower(plain)
	if !strings.Contains(lowerPlain, query) {
		return line
	}

	// Build a mapping from plain-text index to original-string index
	plainToOrig := make([]int, len(plain))
	pi := 0
	for i := 0; i < len(line); {
		if line[i] == '\x1b' && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && !((line[j] >= 'A' && line[j] <= 'Z') || (line[j] >= 'a' && line[j] <= 'z')) {
				j++
			}
			if j < len(line) {
				j++
			}
			i = j
			continue
		}
		if pi < len(plain) {
			plainToOrig[pi] = i
			pi++
		}
		i++
	}

	// Find all match positions in plain text
	type match struct{ start, end int }
	var matches []match
	pos := 0
	for {
		idx := strings.Index(lowerPlain[pos:], query)
		if idx < 0 {
			break
		}
		start := pos + idx
		matches = append(matches, match{start, start + len(query)})
		pos = start + len(query)
	}
	if len(matches) == 0 {
		return line
	}

	hlOn := "\x1b[30;43m" // black on yellow (inactive)
	if active {
		hlOn = "\x1b[30;46m" // black on cyan (active)
	}
	hlOff := "\x1b[0m"
	var result strings.Builder
	lastOrig := 0
	for _, m := range matches {
		origStart := plainToOrig[m.start]
		origEnd := len(line)
		if m.end < len(plainToOrig) {
			origEnd = plainToOrig[m.end]
		}
		result.WriteString(line[lastOrig:origStart])
		result.WriteString(hlOn)
		result.WriteString(line[origStart:origEnd])
		result.WriteString(hlOff)
		lastOrig = origEnd
	}
	result.WriteString(line[lastOrig:])
	return result.String()
}

func (m Model) updateArchive(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Confirmation pattern for delete in archive
	if m.confirmAction != "" {
		isConfirmKey := false
		if m.confirmAction == "delete" {
			isConfirmKey = key.Matches(msg, common.Keys.Delete)
		}
		if !isConfirmKey {
			m.confirmAction = ""
			m.confirmTaskID = ""
		}
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.ToggleArchive):
		m.currentView = viewBoard
		m.confirmAction = ""
		m.confirmTaskID = ""
		return m, nil

	case key.Matches(msg, common.Keys.Quit):
		m.currentView = viewBoard
		m.reload()
		return m, nil

	case key.Matches(msg, common.Keys.Up):
		if m.archiveCursor > 0 {
			m.archiveCursor--
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.archivedTasks()
		if m.archiveCursor < len(tasks)-1 {
			m.archiveCursor++
		}

	case key.Matches(msg, common.Keys.JumpTop):
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.archivedTasks()
		if len(tasks) > 0 {
			m.archiveCursor = len(tasks) - 1
		}

	case key.Matches(msg, common.Keys.Restore):
		t := m.selectedArchiveTask()
		if t != nil {
			m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
			statuses := m.store.GetProjectStatuses(t.Project)
			m.store.MoveTask(t, statuses[0])
			m.reload()
			m.fixArchiveCursor()
		}

	case key.Matches(msg, common.Keys.Delete):
		t := m.selectedArchiveTask()
		if t == nil {
			break
		}
		if m.confirmAction == "delete" && m.confirmTaskID == t.Meta.ID {
			m.lastUndo = &undoAction{kind: "delete", task: snapshotTask(t)}
			m.store.DeleteTask(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
			m.reload()
			m.fixArchiveCursor()
		} else {
			m.confirmAction = "delete"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		t := m.selectedArchiveTask()
		if t != nil {
			m.previousView = viewArchive
			m.currentView = viewDetail
			m.detailTask = t
			m.detailViewport = viewport.New(m.width, m.height-2)
			content := m.renderTaskDetail(t)
			m.detailViewport.SetContent(content)
			m.detailPlainContent = stripANSI(content)
			m.detailSearchQuery = ""
			m.detailSearchMatches = nil
			m.detailSearchIdx = 0
			m.detailSearching = false
		}

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = m.nextVisibleProject(1)
		m.reload()
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = m.nextVisibleProject(-1)
		m.reload()
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.Undo):
		m.doUndo()
		m.fixArchiveCursor()

	case key.Matches(msg, common.Keys.Search):
		m.searching = true
		m.searchInput.SetValue(m.searchQuery)
		m.searchInput.Focus()
		return m, m.searchInput.Cursor.BlinkCmd()
	}
	return m, nil
}

func (m Model) updateFocus(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.FocusView):
		m.currentView = viewBoard
		return m, nil

	case key.Matches(msg, common.Keys.Quit):
		m.currentView = viewBoard
		return m, nil

	case key.Matches(msg, common.Keys.Up):
		if m.focusCursor > 0 {
			m.focusCursor--
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.focusTasks()
		if m.focusCursor < len(tasks)-1 {
			m.focusCursor++
		}

	case key.Matches(msg, common.Keys.JumpTop):
		m.focusCursor = 0

	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.focusTasks()
		if len(tasks) > 0 {
			m.focusCursor = len(tasks) - 1
		}

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		t := m.selectedFocusTask()
		if t != nil {
			m.previousView = viewFocus
			m.currentView = viewDetail
			m.detailTask = t
			m.detailViewport = viewport.New(m.width, m.height-2)
			content := m.renderTaskDetail(t)
			m.detailViewport.SetContent(content)
			m.detailPlainContent = stripANSI(content)
			m.detailSearchQuery = ""
			m.detailSearchMatches = nil
			m.detailSearchIdx = 0
			m.detailSearching = false
		}

	case key.Matches(msg, common.Keys.Focus), key.Matches(msg, common.Keys.Delete):
		t := m.selectedFocusTask()
		if t != nil {
			m.focusPlan.Remove(t.Meta.ID)
			m.focusPlan.Date = storage.Today()
			m.saveFocusPlan()
			m.rebuildFocusSet()
			m.fixFocusCursor()
			m.toastMsg = "Removed from focus"
			m.toastExpiry = time.Now().Add(2 * time.Second)
		}

	case key.Matches(msg, common.Keys.ReorderDown):
		tasks := m.focusTasks()
		if m.focusCursor < len(tasks)-1 {
			m.focusPlan.Swap(m.focusCursor, m.focusCursor+1)
			m.focusCursor++
			m.saveFocusPlan()
		}

	case key.Matches(msg, common.Keys.ReorderUp):
		tasks := m.focusTasks()
		if len(tasks) > 0 && m.focusCursor > 0 {
			m.focusPlan.Swap(m.focusCursor, m.focusCursor-1)
			m.focusCursor--
			m.saveFocusPlan()
		}

	case key.Matches(msg, common.Keys.Move):
		if t := m.selectedFocusTask(); t != nil {
			m.doMoveForward(t)
			m.fixFocusCursor()
		}

	case key.Matches(msg, common.Keys.MoveBack):
		if t := m.selectedFocusTask(); t != nil {
			m.doMoveBack(t)
			m.fixFocusCursor()
		}

	case key.Matches(msg, common.Keys.Done):
		if t := m.selectedFocusTask(); t != nil {
			m.doDone(t)
			m.focusPlan.Remove(t.Meta.ID)
			m.saveFocusPlan()
			m.rebuildFocusSet()
			m.fixFocusCursor()
		}

	case key.Matches(msg, common.Keys.Waiting):
		if t := m.selectedFocusTask(); t != nil {
			m.doWaiting(t)
			m.fixFocusCursor()
		}

	case key.Matches(msg, common.Keys.Undo):
		m.doUndo()
		m.fixFocusCursor()

	case key.Matches(msg, common.Keys.Claude):
		if t := m.selectedFocusTask(); t != nil {
			m.openClaudeMenu(t)
		}

	case key.Matches(msg, common.Keys.Yank):
		if t := m.selectedFocusTask(); t != nil && t.Meta.ID != "" {
			m.copyToClipboard(t.Meta.ID)
		}

	case key.Matches(msg, common.Keys.YankMenu):
		t := m.selectedFocusTask()
		if t != nil {
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

	case key.Matches(msg, common.Keys.Zoom):
		m.zoomed = !m.zoomed
	}
	return m, nil
}

func (m Model) updateYankMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.yankMenu = false
	case key.Matches(msg, common.Keys.Up):
		if m.yankCursor > 0 {
			m.yankCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.yankCursor < len(m.yankItems)-1 {
			m.yankCursor++
		}
	case key.Matches(msg, common.Keys.Enter):
		if m.yankCursor < len(m.yankItems) {
			m.copyToClipboard(m.yankItems[m.yankCursor].value)
		}
		m.yankMenu = false
	}
	return m, nil
}

func (m Model) updateLinksMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.linksMenu = false
	case key.Matches(msg, common.Keys.Up):
		if m.linksCursor > 0 {
			m.linksCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.linksCursor < len(m.linkItems)-1 {
			m.linksCursor++
		}
	case key.Matches(msg, common.Keys.Enter):
		if m.linksCursor < len(m.linkItems) {
			exec.Command("open", m.linkItems[m.linksCursor].url).Start()
		}
		if len(m.linkItems) <= 1 {
			m.linksMenu = false
		}
	case msg.String() == "y":
		if m.linksCursor < len(m.linkItems) {
			m.copyToClipboard(m.linkItems[m.linksCursor].url)
			m.linksMenu = false
		}
	}
	return m, nil
}

func (m *Model) openSessionMenu(t *storage.Task) {
	m.sessionMenuItems = nil
	m.sessionCursor = 0

	// Resolve project dir for CC metadata lookup
	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		projDir = proj.Path
	}

	enrichItem := func(sid string, isLatest bool) sessionMenuItem {
		item := sessionMenuItem{sessionID: sid, isLatest: isLatest}
		if meta := loadSessionMeta(projDir, sid); meta != nil {
			item.summary = meta.Summary
			item.msgCount = meta.MessageCount
			item.branch = meta.GitBranch
			if meta.Modified != "" {
				if t, err := time.Parse(time.RFC3339, meta.Modified); err == nil {
					item.modified = t.Local().Format("Jan 2, 15:04")
				}
			}
		}
		return item
	}

	// Migrate legacy cc-session if present
	if old := t.Meta.Links["cc-session"]; old != "" {
		found := false
		for _, s := range t.Meta.Sessions {
			if s == old {
				found = true
				break
			}
		}
		if !found {
			m.sessionMenuItems = append(m.sessionMenuItems, enrichItem(old, false))
		}
	}

	// Add sessions in reverse order (latest first)
	for i := len(t.Meta.Sessions) - 1; i >= 0; i-- {
		sid := t.Meta.Sessions[i]
		isLatest := i == len(t.Meta.Sessions)-1
		m.sessionMenuItems = append(m.sessionMenuItems, enrichItem(sid, isLatest))
	}

	if len(m.sessionMenuItems) > 0 {
		m.sessionMenu = true
	}
}

func (m Model) updateSessionMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.sessionMenu = false
		m.confirmAction = ""
	case key.Matches(msg, common.Keys.Up):
		if m.sessionCursor > 0 {
			m.sessionCursor--
			m.confirmAction = ""
		}
	case key.Matches(msg, common.Keys.Down):
		if m.sessionCursor < len(m.sessionMenuItems)-1 {
			m.sessionCursor++
			m.confirmAction = ""
		}
	case key.Matches(msg, common.Keys.Enter), msg.String() == "r":
		if m.sessionCursor < len(m.sessionMenuItems) {
			m.resumeSessionID = m.sessionMenuItems[m.sessionCursor].sessionID
			m.sessionMenu = false
			m.resumeOnly = true
			m.forkMode = false
			t := m.selectedTask()
			if t != nil {
				m.openClaudeMenu(t)
			}
			return m, nil
		}
	case msg.String() == "f":
		if m.sessionCursor < len(m.sessionMenuItems) {
			m.resumeSessionID = m.sessionMenuItems[m.sessionCursor].sessionID
			m.sessionMenu = false
			m.resumeOnly = true
			m.forkMode = true
			t := m.selectedTask()
			if t != nil {
				m.openClaudeMenu(t)
			}
			return m, nil
		}
	case msg.String() == "y":
		if m.sessionCursor < len(m.sessionMenuItems) {
			sid := m.sessionMenuItems[m.sessionCursor].sessionID
			m.yankItems = nil
			m.yankCursor = 0
			m.yankItems = append(m.yankItems, yankItem{"id", sid})
			t := m.selectedTask()
			if t != nil {
				var projDir string
				if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
					projDir = proj.Path
				}
				if p := resolveSessionPath(projDir, sid); p != "" {
					m.yankItems = append(m.yankItems, yankItem{"path", p})
				}
			}
			m.yankMenu = true
		}
	case msg.String() == "x":
		if m.sessionCursor >= len(m.sessionMenuItems) {
			break
		}
		if m.confirmAction == "delete-session" {
			// Second press - confirmed
			m.confirmAction = ""
			t := m.selectedTask()
			if t == nil {
				break
			}
			sid := m.sessionMenuItems[m.sessionCursor].sessionID
			newSessions := make([]string, 0, len(t.Meta.Sessions))
			for _, s := range t.Meta.Sessions {
				if s != sid {
					newSessions = append(newSessions, s)
				}
			}
			t.Meta.Sessions = newSessions
			if t.Meta.Links["cc-session"] == sid {
				delete(t.Meta.Links, "cc-session")
				if len(t.Meta.Links) == 0 {
					t.Meta.Links = nil
				}
			}
			t.Meta.Updated = storage.Today()
			m.store.WriteTask(t)
			m.openSessionMenu(t)
			if len(m.sessionMenuItems) == 0 {
				m.sessionMenu = false
			} else if m.sessionCursor >= len(m.sessionMenuItems) {
				m.sessionCursor = len(m.sessionMenuItems) - 1
			}
			m.toastMsg = "Deleted session " + sid[:8] + "..."
			m.toastExpiry = time.Now().Add(2 * time.Second)
			content := m.renderTaskDetail(t)
			m.detailViewport.SetContent(content)
			m.detailPlainContent = stripANSI(content)
		} else {
			// First press - ask for confirmation
			m.confirmAction = "delete-session"
		}
	}
	return m, nil
}

func (m Model) updateColVisMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left), key.Matches(msg, common.Keys.ColumnVis):
		m.colVisMenu = false
		m.reload()
	case key.Matches(msg, common.Keys.Up):
		if m.colVisCursor > 0 {
			m.colVisCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.colVisCursor < len(m.colVisItems)-1 {
			m.colVisCursor++
		}
	case key.Matches(msg, common.Keys.Space):
		if m.colVisCursor < len(m.colVisItems) {
			item := &m.colVisItems[m.colVisCursor]
			item.visible = !item.visible
			// Mark as explicitly set by user
			m.hiddenStatuses[item.status] = !item.visible
		}
	}
	return m, nil
}

func (m *Model) copyToClipboard(value string) {
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader(value)
	c.Start()
	display := value
	if len(display) > 40 {
		display = display[:37] + "..."
	}
	m.toastMsg = "Copied: " + display
	m.toastExpiry = time.Now().Add(2 * time.Second)
}

func (m Model) statusIndex(s storage.TaskStatus) int {
	for i, st := range m.statuses {
		if st == s {
			return i
		}
	}
	return 0
}

func (m *Model) fixCursors() {
	for i := range m.statuses {
		if i >= len(m.cursors) {
			continue
		}
		tasks := m.columnTasks(i)
		if len(tasks) == 0 {
			m.cursors[i] = 0
		} else if m.cursors[i] >= len(tasks) {
			m.cursors[i] = len(tasks) - 1
		}
	}
	m.fixScrollOffsets()
}

// cardHeight returns the rendered height of a single task card (border + content + margin).
func cardHeight(t *storage.Task) int {
	lines := 2 // title + project
	if len(t.Meta.Tags) > 0 {
		lines++
	}
	return lines + 3 // +2 border, +1 margin bottom
}

// fixScrollOffsets ensures the cursor is visible within the column viewport.
// It renders cards to measure actual heights (accounting for text wrapping).
func (m *Model) fixScrollOffsets() {
	overhead := 11
	if m.adding || m.searching || m.searchQuery != "" {
		overhead++
	}
	maxCardHeight := m.height - overhead
	if maxCardHeight < 5 {
		maxCardHeight = 5
	}
	// Conservative budget: subtract 2 for column header + possible scroll indicator
	cardBudget := maxCardHeight - 2
	if cardBudget < 3 {
		cardBudget = 3
	}

	numCols := len(m.statuses)
	if numCols == 0 {
		numCols = 1
	}
	colWidth := (m.width - 8) / numCols
	if m.zoomed {
		colWidth = m.width - 8
	}
	if colWidth < 20 {
		colWidth = 20
	}
	cardW := colWidth - 6

	for i := range m.statuses {
		if i >= len(m.cursors) || i >= len(m.scrollOffsets) {
			continue
		}
		tasks := m.columnTasks(i)
		if len(tasks) == 0 {
			m.scrollOffsets[i] = 0
			continue
		}
		cursor := m.cursors[i]
		if cursor >= len(tasks) {
			cursor = len(tasks) - 1
		}

		measure := func(j int) int {
			// badge/marker live on the existing ID line, so they don't change
			// the line count - pass empty for measurement.
			var card string
			if m.zoomed {
				card = renderZoomCard(tasks[j], cardW, false, "", "")
			} else {
				card = renderCard(tasks[j], cardW, false, "", "")
			}
			return strings.Count(card, "\n") + 1
		}

		// Scroll up if cursor is above the visible window
		if cursor < m.scrollOffsets[i] {
			m.scrollOffsets[i] = cursor
		}

		// Scroll down if cursor is below the visible window
		usedH := 0
		lastVisible := m.scrollOffsets[i]
		for j := m.scrollOffsets[i]; j < len(tasks); j++ {
			h := measure(j)
			if usedH+h > cardBudget && j > m.scrollOffsets[i] {
				break
			}
			usedH += h
			lastVisible = j
		}
		if cursor > lastVisible {
			// Scroll down: find new offset so cursor fits at bottom
			usedH = 0
			start := cursor
			for start >= 0 {
				h := measure(start)
				if usedH+h > cardBudget && start < cursor {
					start++
					break
				}
				usedH += h
				if start == 0 {
					break
				}
				start--
			}
			m.scrollOffsets[i] = start
		}
	}
}

// --- Task action helpers (shared between board/detail/archive views) ---

// fixArchiveCursor clamps archive cursor to valid range after archive list changes.
func (m *Model) fixArchiveCursor() {
	tasks := m.archivedTasks()
	if m.archiveCursor >= len(tasks) && len(tasks) > 0 {
		m.archiveCursor = len(tasks) - 1
	}
}

// doMoveForward cycles task to next status column.
func (m *Model) doMoveForward(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	idx := m.statusIndex(t.Meta.Status)
	m.store.MoveTask(t, m.statuses[(idx+1)%len(m.statuses)])
	m.reload()
}

// doMoveBack cycles task to previous status column.
func (m *Model) doMoveBack(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	idx := m.statusIndex(t.Meta.Status)
	var newStatus storage.TaskStatus
	if idx > 0 {
		newStatus = m.statuses[idx-1]
	} else {
		newStatus = m.statuses[len(m.statuses)-1]
	}
	m.store.MoveTask(t, newStatus)
	m.reload()
}

// doDone marks task with the last status (typically "done").
func (m *Model) doDone(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "done", task: snapshotTask(t)}
	m.store.MoveTask(t, m.statuses[len(m.statuses)-1])
	m.reload()
}

// doWaiting marks task as waiting.
func (m *Model) doWaiting(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	m.store.MoveTask(t, storage.StatusWaiting)
	m.reload()
}

// doArchive archives the task.
func (m *Model) doArchive(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "archive", task: snapshotTask(t)}
	m.store.MoveTask(t, storage.StatusArchived)
	m.reload()
}

// doUndo restores the last undone action.
func (m *Model) doUndo() {
	if m.lastUndo == nil {
		m.toastMsg = "nothing to undo"
		m.toastExpiry = time.Now().Add(2 * time.Second)
		return
	}
	u := m.lastUndo
	m.store.WriteTask(u.task)
	m.lastUndo = nil
	m.toastMsg = "undone: " + u.kind
	m.toastExpiry = time.Now().Add(2 * time.Second)
	m.reload()
}

// doReorder swaps the selected task with a neighbor in the column.
// direction: +1 = move down, -1 = move up.
func (m *Model) doReorder(direction int) {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) < 2 {
		return
	}
	cursor := m.cursors[m.activeCol]
	target := cursor + direction
	if target < 0 || target >= len(tasks) {
		return
	}

	a := tasks[cursor]
	b := tasks[target]

	// If all tasks have Order==0, assign sequential orders to all tasks in column
	allZero := true
	for _, t := range tasks {
		if t.Meta.Order != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		for i, t := range tasks {
			t.Meta.Order = (i + 1) * 10
			t.Meta.Updated = storage.Today()
			m.store.WriteTask(t)
		}
	}

	// Swap orders
	a.Meta.Order, b.Meta.Order = b.Meta.Order, a.Meta.Order
	a.Meta.Updated = storage.Today()
	b.Meta.Updated = storage.Today()
	m.store.WriteTask(a)
	m.store.WriteTask(b)

	m.cursors[m.activeCol] = target
	m.reload()
}

func (m *Model) openClaudeMenu(t *storage.Task) {
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0
	m.claudeMenuSkipPerms = false
	m.launchAgent = launchAgentClaude
	m.rebuildClaudeMenuItems(t)
	m.claudeMenu = true
}

// openExecutorMenu opens the launch overlay directly in executor mode for task t
// (which may be a tracker -> run-epic, or a leaf task -> work). Reuses the same
// overlay as the Claude/Codex launch menu.
func (m *Model) openExecutorMenu(t *storage.Task) {
	if t == nil {
		return
	}
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0
	m.claudeMenuSkipPerms = false
	m.launchAgent = launchAgentExecutor
	m.resumeOnly = false
	m.forkMode = false
	m.projectScopeLaunch = false
	m.projectScopeSlug = ""
	m.rebuildClaudeMenuItems(t)
	m.claudeMenu = true
}

// menuTask returns the task the launch overlay acts on: the detail task when in
// the detail view (correct even after a relation jump), otherwise the
// board-selected task.
func (m Model) menuTask() *storage.Task {
	if m.currentView == viewDetail && m.detailTask != nil {
		return m.detailTask
	}
	return m.selectedTask()
}

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

func (m *Model) rebuildClaudeMenuItems(t *storage.Task) {
	inTmux := os.Getenv("TMUX") != ""
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0

	if m.projectScopeLaunch {
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Here (takes over terminal)", "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Tmux window", "tmux", "t"})
			m.claudeMenuCursor = 1
		}
		return
	}

	if m.launchAgent == launchAgentExecutor {
		// Executor runs `pm work` (task) or `pm run-epic` (tracker). Long-running,
		// so default to tmux when available. No worktree/resume/fork options.
		m.executorIsTracker = len(m.taskChildren(t)) > 0
		bgLabel := "Run task in background (watch in pm)"
		runLabel := "Run task here (pm work)"
		tmuxLabel := "Run task in tmux (pm work)"
		if m.executorIsTracker {
			bgLabel = "Run epic in background (watch in pm)"
			runLabel = "Run epic here (pm run-epic)"
			tmuxLabel = "Run epic in tmux (pm run-epic)"
		}
		// Background is the default: non-blocking, observable natively in pm.
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{bgLabel, "bg", "b"})
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{runLabel, "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{tmuxLabel, "tmux", "t"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Dry-run preview", "dry-run", "d"})
		m.claudeMenuCursor = 0 // default to background
		return
	}

	if m.launchAgent == launchAgentCodex {
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Here (takes over terminal)", "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Tmux window", "tmux", "t"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree (here)", "worktree", "w"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree + tmux", "worktree-tmux", "W"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Project scope (no task)", "project", "p"})
		if inTmux {
			m.claudeMenuCursor = 1
		}
		return
	}

	if m.resumeOnly && m.forkMode {
		// Fork sub-menu: fork from selected session
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Fork here", "fork", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Fork in tmux", "fork-tmux", "t"})
			m.claudeMenuCursor = 1 // default to tmux
		}
		return
	}

	hasSession := t != nil && lastSession(t) != ""
	if m.resumeOnly {
		// Resume sub-menu: only show resume options
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume here", "resume", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume in tmux", "resume-tmux", "t"})
			m.claudeMenuCursor = 1 // default to tmux
		}
	} else {
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Here (takes over terminal)", "here", "h"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Tmux window", "tmux", "t"})
		}
		if hasSession {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume session", "resume", "r"})
			if inTmux {
				m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Resume in tmux", "resume-tmux", "R"})
			}
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree (here)", "worktree", "w"})
		if inTmux {
			m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Worktree + tmux", "worktree-tmux", "W"})
		}
		m.claudeMenuItems = append(m.claudeMenuItems, claudeMenuItem{"Project scope (no task)", "project", "p"})
		// Smart default: tmux if available, else here
		if inTmux {
			for i, item := range m.claudeMenuItems {
				if item.kind == "tmux" {
					m.claudeMenuCursor = i
					break
				}
			}
		}
	}
}

func (m *Model) openProjectClaudeMenu(slug string) {
	m.openProjectClaudeMenuWithAgent(slug, launchAgentClaude)
}

func (m *Model) openProjectClaudeMenuWithAgent(slug string, agent launchAgent) {
	m.claudeMenuItems = nil
	m.claudeMenuCursor = 0
	m.claudeMenuSkipPerms = false
	m.launchAgent = agent
	m.resumeOnly = false
	m.forkMode = false
	m.projectScopeLaunch = true
	m.projectScopeSlug = slug
	m.rebuildClaudeMenuItems(nil)
	m.claudeMenu = true
}

func (m Model) updateClaudeMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Toggle skip-permissions
	typed := msg.String()
	if typed == "!" {
		m.claudeMenuSkipPerms = !m.claudeMenuSkipPerms
		return m, nil
	}
	if typed == "@" {
		if m.projectScopeLaunch {
			// Project scope has no task: executor is not applicable, cycle claude<->codex.
			if m.launchAgent == launchAgentCodex {
				m.launchAgent = launchAgentClaude
			} else {
				m.launchAgent = launchAgentCodex
			}
		} else {
			// Task scope: cycle claude -> codex -> executor -> claude.
			switch m.launchAgent {
			case launchAgentClaude:
				m.launchAgent = launchAgentCodex
			case launchAgentCodex:
				m.launchAgent = launchAgentExecutor
			default:
				m.launchAgent = launchAgentClaude
			}
		}
		m.rebuildClaudeMenuItems(m.menuTask())
		return m, nil
	}

	// Check mnemonic shortcut keys first
	for _, item := range m.claudeMenuItems {
		if typed == item.shortcut {
			m.claudeMenu = false
			m.resumeOnly = false
			m.forkMode = false
			return m.launchLLM(item.kind)
		}
	}

	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.claudeMenu = false
		m.resumeOnly = false
		m.forkMode = false
		m.resumeSessionID = ""
		m.projectScopeLaunch = false
		m.projectScopeSlug = ""
	case key.Matches(msg, common.Keys.Up):
		if m.claudeMenuCursor > 0 {
			m.claudeMenuCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.claudeMenuCursor < len(m.claudeMenuItems)-1 {
			m.claudeMenuCursor++
		}
	case key.Matches(msg, common.Keys.Enter):
		if m.claudeMenuCursor < len(m.claudeMenuItems) {
			item := m.claudeMenuItems[m.claudeMenuCursor]
			m.claudeMenu = false
			m.resumeOnly = false
			m.forkMode = false
			return m.launchLLM(item.kind)
		}
	}
	return m, nil
}

// worktreeName returns a semantic name for the worktree branch.
// Uses task branch if set, otherwise slugifies the task title.
func worktreeName(t *storage.Task) string {
	if t.Meta.Branch != "" {
		return t.Meta.Branch
	}
	return storage.Slugify(t.Meta.Title)
}

func tmuxWindowName(prefix, taskID, sessionID string) string {
	short := sessionID
	if len(short) > 4 {
		short = short[:4]
	}
	return fmt.Sprintf("%s:%s:%s", prefix, short, taskID)
}

func tmuxGetWindowName() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#W").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func tmuxRenameWindow(name string) {
	if name != "" {
		exec.Command("tmux", "rename-window", name).Run()
	}
}

// tmuxSessionForProc walks the process tree up from the current pid and
// returns the session name of the tmux pane whose pane_pid matches an
// ancestor. This is more reliable than $TMUX, which becomes stale when a
// window is moved between sessions after the shell started. Returns "" if
// the process is not inside any detectable tmux pane.
func tmuxSessionForProc() string {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid} #{session_name}").Output()
	if err != nil {
		return ""
	}
	panes := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
		if len(parts) != 2 {
			continue
		}
		if pid, err := strconv.Atoi(parts[0]); err == nil {
			panes[pid] = parts[1]
		}
	}
	pid := os.Getpid()
	for i := 0; i < 32 && pid > 1; i++ {
		if sess, ok := panes[pid]; ok {
			return sess
		}
		ppid, err := tmuxParentPID(pid)
		if err != nil || ppid == 0 || ppid == pid {
			return ""
		}
		pid = ppid
	}
	return ""
}

func tmuxParentPID(pid int) (int, error) {
	out, err := exec.Command("ps", "-o", "ppid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// tmuxNewWindow creates a new tmux window running shellCmd via sh -c.
// Targets the detected session explicitly so the window appears where
// the user can see it (not where $TMUX env happens to point). Returns
// the target session name and any error from tmux (with stderr content
// surfaced in the error message).
func tmuxNewWindow(name, shellCmd string) (string, error) {
	sess := tmuxSessionForProc()
	args := []string{"new-window", "-n", name}
	if sess != "" {
		args = append(args, "-t", sess+":")
	}
	args = append(args, "sh", "-c", shellCmd)
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return sess, fmt.Errorf("%s", msg)
	}
	return sess, nil
}

func tmuxLaunchToast(label, winName, sess string) string {
	if sess != "" {
		return fmt.Sprintf("%s [%s]: %s", label, sess, winName)
	}
	return label + ": " + winName
}

// copyWorktreeFiles pre-creates a git worktree (if needed) and copies all
// untracked files from the main repo into the worktree.
func copyWorktreeFiles(projDir, wtName string) {
	wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)

	// Pre-create worktree if it doesn't exist yet
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		cmd := exec.Command("git", "worktree", "add", wtPath)
		cmd.Dir = projDir
		if err := cmd.Run(); err != nil {
			return
		}
	}

	// Find all untracked files (both ignored and non-ignored)
	cmd := exec.Command("git", "ls-files", "--others")
	cmd.Dir = projDir
	out, err := cmd.Output()
	if err != nil {
		return
	}

	for _, rel := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if rel == "" {
			continue
		}
		src := filepath.Join(projDir, rel)
		dst := filepath.Join(wtPath, rel)

		// Skip if destination already exists
		if _, err := os.Stat(dst); err == nil {
			continue
		}

		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		info, _ := os.Stat(src)
		os.MkdirAll(filepath.Dir(dst), 0755)
		os.WriteFile(dst, data, info.Mode())
	}
}

func codexArgs(prompt string, bypass bool) []string {
	args := []string{}
	if bypass {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	return append(args, prompt)
}

func codexShellCommand(prompt string, bypass bool) string {
	args := codexArgs(prompt, bypass)
	parts := []string{"codex"}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func codexInteractiveShellCommand(prompt string, bypass bool) string {
	return codexShellCommand(prompt, bypass) + "; status=$?; if [ $status -ne 0 ]; then printf '\\n[pm] Codex exited with status %s. Press Enter to return to board...' \"$status\"; read _; fi; exit $status"
}

func codexWindowName(id string) string {
	return "cx:" + id
}

func (m Model) launchLLM(kind string) (tea.Model, tea.Cmd) {
	switch m.launchAgent {
	case launchAgentCodex:
		return m.launchCodex(kind)
	case launchAgentExecutor:
		return m.launchExecutor(kind)
	}
	return m.launchClaude(kind)
}

func (m Model) launchProjectCodex(kind string) (tea.Model, tea.Cmd) {
	m.projectScopeLaunch = false
	slug := m.projectScopeSlug
	m.projectScopeSlug = ""

	proj, err := m.store.GetProject(slug)
	if err != nil || proj == nil {
		return m, nil
	}

	prompt := buildProjectPrompt(proj, slug)

	var projDir string
	if proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}

	switch kind {
	case "here":
		c := exec.Command("sh", "-c", codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		if projDir != "" {
			c.Dir = projDir
		}
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(codexWindowName(slug))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentCodex, kind: "project here", err: err}
		})

	case "tmux":
		shellCmd := codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms)
		if projDir != "" {
			shellCmd = fmt.Sprintf("cd %s && %s", shellQuote(projDir), shellCmd)
		}
		winName := codexWindowName(slug)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "Codex tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched Codex in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
}

func (m Model) launchCodex(kind string) (tea.Model, tea.Cmd) {
	if m.projectScopeLaunch {
		return m.launchProjectCodex(kind)
	}

	t := m.selectedTask()
	if t == nil {
		return m, nil
	}

	if kind == "project" {
		m.openProjectClaudeMenuWithAgent(t.Project, launchAgentCodex)
		return m, nil
	}

	prompt := buildClaudePrompt(t, m.store)

	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}

	setDir := func(c *exec.Cmd) {
		if projDir != "" {
			c.Dir = projDir
		}
	}

	withCd := func(cmd string) string {
		if projDir != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(projDir), cmd)
		}
		return cmd
	}

	worktreeDir := func() string {
		if projDir == "" {
			return ""
		}
		wtName := worktreeName(t)
		copyWorktreeFiles(projDir, wtName)
		wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)
		if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
			return wtPath
		}
		return ""
	}

	withWorktreeCd := func(cmd string) string {
		if wtDir := worktreeDir(); wtDir != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(wtDir), cmd)
		}
		return withCd(cmd)
	}

	switch kind {
	case "here":
		c := exec.Command("sh", "-c", codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		setDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(codexWindowName(t.Meta.ID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentCodex, kind: "here", err: err}
		})

	case "tmux":
		shellCmd := withCd(codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		winName := codexWindowName(t.Meta.ID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "Codex tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched Codex in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case "worktree":
		wtDir := worktreeDir()
		c := exec.Command("sh", "-c", codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		if wtDir != "" {
			c.Dir = wtDir
		} else {
			setDir(c)
		}
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(codexWindowName(t.Meta.ID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentCodex, kind: "worktree", err: err}
		})

	case "worktree-tmux":
		shellCmd := withWorktreeCd(codexInteractiveShellCommand(prompt, m.claudeMenuSkipPerms))
		winName := codexWindowName(t.Meta.ID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "Codex worktree tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched Codex worktree in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
}

func (m Model) launchProjectClaude(kind string) (tea.Model, tea.Cmd) {
	m.projectScopeLaunch = false
	slug := m.projectScopeSlug
	m.projectScopeSlug = ""

	proj, err := m.store.GetProject(slug)
	if err != nil || proj == nil {
		return m, nil
	}

	prompt := buildProjectPrompt(proj, slug)
	skipFlag := ""
	if m.claudeMenuSkipPerms {
		skipFlag = "--dangerously-skip-permissions"
	}

	var projDir string
	if proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}

	sessionID := generateSessionID()

	switch kind {
	case "here":
		args := []string{"--session-id", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		args = append(args, prompt)
		c := exec.Command("claude", args...)
		if projDir != "" {
			c.Dir = projDir
		}
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", slug, sessionID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "tmux":
		withCd := func(cmd string) string {
			if projDir != "" {
				return fmt.Sprintf("cd %s && %s", shellQuote(projDir), cmd)
			}
			return cmd
		}
		shellCmd := withCd(fmt.Sprintf("claude --session-id %s %s %s", sessionID, skipFlag, shellQuote(prompt)))
		winName := fmt.Sprintf("cc:%s:%s", sessionID[:4], slug)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
}

func (m Model) launchClaude(kind string) (tea.Model, tea.Cmd) {
	if m.projectScopeLaunch {
		return m.launchProjectClaude(kind)
	}

	t := m.selectedTask()
	if t == nil {
		return m, nil
	}

	prompt := buildClaudePrompt(t, m.store)
	skipFlag := ""
	if m.claudeMenuSkipPerms {
		skipFlag = "--dangerously-skip-permissions"
	}

	// Resolve project working directory
	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}

	// Check if the session lives in a worktree. Claude Code stores conversations
	// in ~/.claude/projects/<path-hash>/ where path-hash is derived from cwd.
	// When a session was created inside a worktree, we need to resume from that
	// worktree dir so CC finds the conversation.
	var worktreeDir string
	var sessionDir string // fallback: global scan for session file
	if kind == "resume" || kind == "resume-tmux" || kind == "fork" || kind == "fork-tmux" {
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		if projDir != "" {
			worktreeDir = findWorktreeForSession(projDir, sessionID)
		}
		// Fallback: if session not found via projDir (or projDir is empty),
		// scan all CC project dirs globally.
		if worktreeDir == "" && resolveSessionPath(projDir, sessionID) == "" {
			sessionDir = findSessionDirGlobal(sessionID, m.store)
		}
	}

	setDir := func(c *exec.Cmd) {
		if projDir != "" {
			c.Dir = projDir
		}
	}

	resumeDir := func() string {
		if worktreeDir != "" {
			return worktreeDir
		}
		if projDir != "" && sessionDir == "" {
			return projDir
		}
		if sessionDir != "" {
			return sessionDir
		}
		return ""
	}

	setResumeDir := func(c *exec.Cmd) {
		if d := resumeDir(); d != "" {
			c.Dir = d
		}
	}

	withCd := func(cmd string) string {
		if projDir != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(projDir), cmd)
		}
		return cmd
	}

	withResumeCd := func(cmd string) string {
		if d := resumeDir(); d != "" {
			return fmt.Sprintf("cd %s && %s", shellQuote(d), cmd)
		}
		return cmd
	}

	switch kind {
	case "project":
		m.openProjectClaudeMenu(t.Project)
		return m, nil

	case "here":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		args := []string{"--session-id", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		args = append(args, prompt)
		c := exec.Command("claude", args...)
		setDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", t.Meta.ID, sessionID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "tmux":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		shellCmd := withCd(fmt.Sprintf("claude --session-id %s %s %s", sessionID, skipFlag, shellQuote(prompt)))
		winName := tmuxWindowName("cc", t.Meta.ID, sessionID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case "worktree":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		wtName := worktreeName(t)
		if projDir != "" {
			copyWorktreeFiles(projDir, wtName)
		}
		args := []string{"-w", wtName, "--session-id", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		args = append(args, prompt)
		c := exec.Command("claude", args...)
		setDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("wt", t.Meta.ID, sessionID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "worktree-tmux":
		sessionID := generateSessionID()
		m.saveSession(t, sessionID)
		wtName := worktreeName(t)
		if projDir != "" {
			copyWorktreeFiles(projDir, wtName)
		}
		shellCmd := withCd(fmt.Sprintf("claude -w %s --session-id %s %s %s", shellQuote(wtName), sessionID, skipFlag, shellQuote(prompt)))
		winName := tmuxWindowName("wt", t.Meta.ID, sessionID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched worktree in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case "resume":
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		m.resumeSessionID = ""
		args := []string{"--resume", sessionID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		c := exec.Command("claude", args...)
		setResumeDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", t.Meta.ID, sessionID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "resume-tmux":
		sessionID := m.resumeSessionID
		if sessionID == "" {
			sessionID = lastSession(t)
		}
		m.resumeSessionID = ""
		shellCmd := withResumeCd(fmt.Sprintf("claude --resume %s %s", sessionID, skipFlag))
		winName := tmuxWindowName("cc", t.Meta.ID, sessionID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Resumed in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}

	case "fork":
		parentID := m.resumeSessionID
		if parentID == "" {
			parentID = lastSession(t)
		}
		m.resumeSessionID = ""
		m.forkMode = false
		newID := generateSessionID()
		m.saveSession(t, newID)
		args := []string{"--resume", parentID, "--fork-session", "--session-id", newID}
		if skipFlag != "" {
			args = append(args, skipFlag)
		}
		c := exec.Command("claude", args...)
		setResumeDir(c)
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(tmuxWindowName("cc", t.Meta.ID, newID))
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return reloadMsg{}
		})

	case "fork-tmux":
		parentID := m.resumeSessionID
		if parentID == "" {
			parentID = lastSession(t)
		}
		m.resumeSessionID = ""
		m.forkMode = false
		newID := generateSessionID()
		m.saveSession(t, newID)
		shellCmd := withResumeCd(fmt.Sprintf("claude --resume %s --fork-session --session-id %s %s", parentID, newID, skipFlag))
		winName := tmuxWindowName("cc", t.Meta.ID, newID)
		sess, err := tmuxNewWindow(winName, shellCmd)
		if err != nil {
			m.toastMsg = "tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Forked in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
}

// executorPMArgs builds the `pm` argv for the executor launch: `work` for a leaf
// task, `run-epic` for a tracker, plus optional --yolo / --dry-run. Pure so it
// can be unit-tested without spawning a process.
func executorPMArgs(t *storage.Task, isTracker, yolo, dryRun bool) []string {
	sub := "work"
	if isTracker {
		sub = "run-epic"
	}
	args := []string{sub, t.Project, t.Meta.ID}
	if yolo {
		args = append(args, "--yolo")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

// pmShellCommand renders `cd <dir> && pm <args...>` with each token shell-quoted.
func pmShellCommand(dir string, args []string) string {
	parts := []string{"pm"}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return fmt.Sprintf("cd %s && %s", shellQuote(dir), strings.Join(parts, " "))
}

// Pause tails keep the terminal/tmux window readable after the executor exits.
// execPauseOnError waits only on a non-zero exit (used for "here" runs that
// return to the board); execPauseAlways always waits (tmux windows and dry-run
// previews, where the output is the whole point).
const execPauseOnError = "; status=$?; if [ $status -ne 0 ]; then printf '\\n[pm] executor exited with status %s. Press Enter to return to board...' \"$status\"; read _; fi; exit $status"
const execPauseAlways = "; status=$?; printf '\\n[pm] executor exited (status %s). Press Enter to close...' \"$status\"; read _"

// boardGitPreflight returns a human-readable reason the executor cannot run in
// dir, or "" if the preconditions (git repo, clean tree) are satisfied.
func boardGitPreflight(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		return "not a git repo: " + dir
	}
	st, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err == nil && strings.TrimSpace(string(st)) != "" {
		return "working tree dirty - commit or stash first"
	}
	return ""
}

// launchExecutor runs `pm work`/`pm run-epic` for the menu task. kind is one of
// "here", "tmux", "dry-run". Reuses the launch overlay's skip-perms toggle as
// --yolo. Long runs default to tmux; the board reflects the resulting
// status/brief on reload (launchResultMsg triggers m.reload()).
func (m Model) launchExecutor(kind string) (tea.Model, tea.Cmd) {
	t := m.menuTask()
	if t == nil {
		return m, nil
	}

	// Resolve project working directory.
	var projDir string
	if proj, err := m.store.GetProject(t.Project); err == nil && proj.Path != "" {
		if info, err := os.Stat(proj.Path); err == nil && info.IsDir() {
			projDir = proj.Path
		}
	}
	if projDir == "" {
		m.toastMsg = "executor: project has no valid path - set it in project.yaml"
		m.toastExpiry = time.Now().Add(15 * time.Second)
		return m, nil
	}

	isTracker := len(m.taskChildren(t)) > 0
	dryRun := kind == "dry-run"
	args := executorPMArgs(t, isTracker, m.claudeMenuSkipPerms, dryRun)
	winName := "pm:" + args[0] + ":" + t.Meta.ID

	// Real runs (here/tmux) require a clean git repo; dry-run is side-effect-free.
	if !dryRun {
		if reason := boardGitPreflight(projDir); reason != "" {
			m.toastMsg = "executor: " + reason
			m.toastExpiry = time.Now().Add(15 * time.Second)
			return m, nil
		}
	}

	switch kind {
	case "bg":
		// Detached background run, output to a log file, observable natively in
		// pm (run-state + agent-view). Does not take over the terminal or need tmux.
		// Run-state + log live in the pm data dir (where the board reads them);
		// the worker still runs in the git repo (c.Dir = projDir).
		stateDir := m.store.ProjectDir(t.Project)
		logPath := storage.ExecutorLogPath(stateDir, t.Meta.ID)
		if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
			m.toastMsg = "executor: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
			return m, nil
		}
		logf, err := os.Create(logPath)
		if err != nil {
			m.toastMsg = "executor: cannot open log: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
			return m, nil
		}
		c := exec.Command("pm", args...)
		c.Dir = projDir
		c.Stdout = logf
		c.Stderr = logf
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach: survive board exit, no controlling tty
		err = c.Start()
		logf.Close() // the child keeps its own dup'd fd
		if err != nil {
			m.toastMsg = "executor: failed to start: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
			return m, nil
		}
		// Seed run-state so the board reflects the run immediately; the executor
		// process overwrites it with richer progress (subs/phase/session) as it goes.
		_ = storage.WriteRunState(stateDir, &storage.RunState{
			TaskID:   t.Meta.ID,
			Project:  t.Project,
			Kind:     args[0],
			Status:   storage.RunStatusRunning,
			PID:      c.Process.Pid,
			RepoPath: projDir,
			LogPath:  logPath,
			Started:  time.Now().UTC().Format(time.RFC3339),
		})
		m.refreshRunStates()
		m.toastMsg = fmt.Sprintf("Started %s %s in background (▶ on the board)", args[0], t.Meta.ID)
		m.toastExpiry = time.Now().Add(4 * time.Second)
		return m, nil

	case "here":
		c := exec.Command("sh", "-c", pmShellCommand(projDir, args)+execPauseOnError)
		c.Dir = projDir
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(winName)
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentExecutor, kind: "here", err: err}
		})

	case "dry-run":
		c := exec.Command("sh", "-c", pmShellCommand(projDir, args)+execPauseAlways)
		c.Dir = projDir
		origWin := tmuxGetWindowName()
		tmuxRenameWindow(winName + ":dry")
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			tmuxRenameWindow(origWin)
			return launchResultMsg{agent: launchAgentExecutor, kind: "dry-run", err: err}
		})

	case "tmux":
		sess, err := tmuxNewWindow(winName, pmShellCommand(projDir, args)+execPauseAlways)
		if err != nil {
			m.toastMsg = "executor tmux failed: " + err.Error()
			m.toastExpiry = time.Now().Add(15 * time.Second)
		} else {
			m.toastMsg = tmuxLaunchToast("Launched "+args[0]+" in tmux", winName, sess)
			m.toastExpiry = time.Now().Add(3 * time.Second)
		}
	}

	return m, nil
}

func buildClaudePrompt(t *storage.Task, store storage.TaskStore) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Working on: #%s %s [%s]\n", t.Meta.ID, t.Meta.Title, t.Meta.Status)
	fmt.Fprintf(&sb, "Project: %s\n", t.Project)
	if t.Meta.Branch != "" {
		fmt.Fprintf(&sb, "Branch: %s\n", t.Meta.Branch)
	}

	if proj, err := store.GetProject(t.Project); err == nil {
		if proj.Path != "" {
			fmt.Fprintf(&sb, "Path: %s\n", proj.Path)
		}
		if proj.Stack != "" {
			fmt.Fprintf(&sb, "Stack: %s\n", proj.Stack)
		}
		if proj.Repo != "" {
			fmt.Fprintf(&sb, "Repo: %s\n", proj.Repo)
		}
		if proj.Notes != "" {
			fmt.Fprintf(&sb, "Notes: %s\n", proj.Notes)
		}
	}

	if t.Meta.Brief != "" {
		fmt.Fprintf(&sb, "\nBrief: %s\n", t.Meta.Brief)
	}

	if t.Meta.AC != "" {
		fmt.Fprintf(&sb, "\nAcceptance Criteria:\n%s\n", t.Meta.AC)
	}

	if body := strings.TrimSpace(t.Body); body != "" {
		fmt.Fprintf(&sb, "\n---\n%s\n", body)
	}

	return sb.String()
}

func buildProjectPrompt(proj *storage.Project, slug string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Project: %s\n", slug)
	if proj.Name != "" {
		fmt.Fprintf(&sb, "Name: %s\n", proj.Name)
	}
	if proj.Path != "" {
		fmt.Fprintf(&sb, "Path: %s\n", proj.Path)
	}
	if proj.Stack != "" {
		fmt.Fprintf(&sb, "Stack: %s\n", proj.Stack)
	}
	if proj.Repo != "" {
		fmt.Fprintf(&sb, "Repo: %s\n", proj.Repo)
	}
	if proj.Notes != "" {
		fmt.Fprintf(&sb, "Notes: %s\n", proj.Notes)
	}
	return sb.String()
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func lastSession(t *storage.Task) string {
	if len(t.Meta.Sessions) > 0 {
		return t.Meta.Sessions[len(t.Meta.Sessions)-1]
	}
	return t.Meta.Links["cc-session"] // legacy fallback
}

func (m *Model) saveSession(t *storage.Task, sessionID string) {
	// Migrate legacy cc-session link
	if old := t.Meta.Links["cc-session"]; old != "" {
		if len(t.Meta.Sessions) == 0 || t.Meta.Sessions[len(t.Meta.Sessions)-1] != old {
			t.Meta.Sessions = append(t.Meta.Sessions, old)
		}
		delete(t.Meta.Links, "cc-session")
		if len(t.Meta.Links) == 0 {
			t.Meta.Links = nil
		}
	}
	t.Meta.Sessions = append(t.Meta.Sessions, sessionID)
	t.Meta.Updated = storage.Today()
	m.store.WriteTask(t)
}

type sessionIndexEntry struct {
	SessionID    string `json:"sessionId"`
	Summary      string `json:"summary"`
	MessageCount int    `json:"messageCount"`
	Modified     string `json:"modified"`
	GitBranch    string `json:"gitBranch"`
}

type sessionsIndex struct {
	Entries []sessionIndexEntry `json:"entries"`
}

// ccProjectDirs returns CC project directories to search for session data.
// Includes main project dir and all worktree dirs.
func ccProjectDirs(projDir string) []string {
	homeDir, _ := os.UserHomeDir()
	ccProjectsDir := filepath.Join(homeDir, ".claude", "projects")

	var dirs []string
	dirs = append(dirs, filepath.Join(ccProjectsDir, pathToCCProject(projDir)))
	wtBase := filepath.Join(projDir, ".claude", "worktrees")
	if entries, err := os.ReadDir(wtBase); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				wtPath := filepath.Join(wtBase, e.Name())
				dirs = append(dirs, filepath.Join(ccProjectsDir, pathToCCProject(wtPath)))
			}
		}
	}
	return dirs
}

// resolveSessionPath returns the absolute path to the session JSONL file,
// or empty string if the file cannot be found.
func resolveSessionPath(projDir, sessionID string) string {
	if projDir == "" || sessionID == "" {
		return ""
	}
	for _, dir := range ccProjectDirs(projDir) {
		p := filepath.Join(dir, sessionID+".jsonl")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// loadSessionMeta looks up session metadata from Claude Code's sessions-index.json.
// Falls back to parsing the JSONL file directly if not found in index.
func loadSessionMeta(projDir, sessionID string) *sessionIndexEntry {
	if projDir == "" || sessionID == "" {
		return nil
	}

	ccDirs := ccProjectDirs(projDir)

	// Try sessions-index.json first (fast path)
	for _, dir := range ccDirs {
		indexPath := filepath.Join(dir, "sessions-index.json")
		data, err := os.ReadFile(indexPath)
		if err != nil {
			continue
		}
		var idx sessionsIndex
		if err := json.Unmarshal(data, &idx); err != nil {
			continue
		}
		for _, e := range idx.Entries {
			if e.SessionID == sessionID {
				return &e
			}
		}
	}

	// Fallback: parse JSONL file directly
	for _, dir := range ccDirs {
		jsonlPath := filepath.Join(dir, sessionID+".jsonl")
		info, err := os.Stat(jsonlPath)
		if err != nil {
			continue
		}
		entry := &sessionIndexEntry{
			SessionID: sessionID,
			Modified:  info.ModTime().UTC().Format(time.RFC3339),
		}
		data, err := os.ReadFile(jsonlPath)
		if err != nil {
			return entry
		}
		msgCount := 0
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var rec struct {
				Type    string `json:"type"`
				Summary string `json:"summary"`
			}
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue
			}
			if rec.Type == "user" || rec.Type == "assistant" {
				msgCount++
			}
			if rec.Type == "summary" && rec.Summary != "" {
				entry.Summary = rec.Summary
			}
		}
		entry.MessageCount = msgCount
		return entry
	}

	return nil
}

// findWorktreeForSession scans worktree subdirs under projDir and checks
// if Claude Code has a conversation file for the given session ID stored
// under a project directory corresponding to that worktree path.
// Returns the worktree absolute path if found, empty string otherwise.
func findWorktreeForSession(projDir, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	wtBase := filepath.Join(projDir, ".claude", "worktrees")
	entries, err := os.ReadDir(wtBase)
	if err != nil {
		return ""
	}
	homeDir, _ := os.UserHomeDir()
	ccProjectsDir := filepath.Join(homeDir, ".claude", "projects")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtPath := filepath.Join(wtBase, e.Name())
		// Claude Code maps absolute path to project dir name by replacing
		// "/" with "-" and stripping "." from directory names.
		ccDirName := pathToCCProject(wtPath)
		sessionFile := filepath.Join(ccProjectsDir, ccDirName, sessionID+".jsonl")
		if _, err := os.Stat(sessionFile); err == nil {
			return wtPath
		}
	}
	return ""
}

// findSessionDirGlobal scans all CC project directories (~/.claude/projects/*)
// for a session JSONL file. Returns the filesystem path that CC would need as
// cwd to find the session, or empty string if not found.
// Uses forward-matching: encodes candidate paths and compares against the CC dir
// name (decoding is ambiguous because pathToCCProject is lossy).
func findSessionDirGlobal(sessionID string, store storage.TaskStore) string {
	if sessionID == "" {
		return ""
	}
	homeDir, _ := os.UserHomeDir()
	ccProjectsDir := filepath.Join(homeDir, ".claude", "projects")

	// Find which CC project dir has the session file
	var ccDirName string
	entries, err := os.ReadDir(ccProjectsDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(ccProjectsDir, e.Name(), sessionID+".jsonl")
		if _, err := os.Stat(p); err == nil {
			ccDirName = e.Name()
			break
		}
	}
	if ccDirName == "" {
		return ""
	}

	// Build candidate paths and check which one encodes to ccDirName.
	// 1. PM project paths
	if slugs, err := store.ListActiveProjects(); err == nil {
		for _, slug := range slugs {
			if proj, err := store.GetProject(slug); err == nil && proj.Path != "" {
				if abs, err := filepath.Abs(proj.Path); err == nil {
					if pathToCCProject(abs) == ccDirName {
						return abs
					}
				}
			}
		}
	}

	// 2. Common directories: ~, ~/*, ~/.claude/*
	for _, parent := range []string{homeDir, filepath.Join(homeDir, ".claude")} {
		if pathToCCProject(parent) == ccDirName {
			return parent
		}
		if children, err := os.ReadDir(parent); err == nil {
			for _, c := range children {
				if c.IsDir() {
					candidate := filepath.Join(parent, c.Name())
					if pathToCCProject(candidate) == ccDirName {
						return candidate
					}
				}
			}
		}
	}

	return ""
}

// pathToCCProject converts an absolute path to the Claude Code project
// directory name format: replace "/" and "." with "-".
func pathToCCProject(absPath string) string {
	s := strings.ReplaceAll(absPath, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func openEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}
	c := exec.Command(editor, path)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return reloadMsg{}
	})
}

// openSubtaskPicker opens the overlay listing a tracker's children for
// arrow-key selection (used when pressing p on a parent task in the detail view).
func (m *Model) openSubtaskPicker(kids []*storage.Task) {
	m.subtaskItems = kids
	m.subtaskCursor = 0
	m.subtaskPicker = true
}

func (m Model) updateSubtaskPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Left):
		m.subtaskPicker = false
	case key.Matches(msg, common.Keys.Up):
		if m.subtaskCursor > 0 {
			m.subtaskCursor--
		}
	case key.Matches(msg, common.Keys.Down):
		if m.subtaskCursor < len(m.subtaskItems)-1 {
			m.subtaskCursor++
		}
	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Right):
		if m.subtaskCursor < len(m.subtaskItems) {
			target := m.subtaskItems[m.subtaskCursor]
			m.subtaskPicker = false
			m.openDetailTask(target)
		}
	}
	return m, nil
}

// openDetailTask loads a task into the detail viewport (used by Enter and by
// parent<->subtask navigation). Leaves previousView untouched so Esc returns to
// wherever the detail flow started.
func (m *Model) openDetailTask(t *storage.Task) {
	m.detailTask = t
	m.detailViewport = viewport.New(m.width, m.height-2)
	content := m.renderTaskDetail(t)
	m.detailViewport.SetContent(content)
	m.detailPlainContent = stripANSI(content)
	m.detailSearchQuery = ""
	m.detailSearchMatches = nil
	m.detailSearchIdx = 0
	m.detailSearching = false
}
