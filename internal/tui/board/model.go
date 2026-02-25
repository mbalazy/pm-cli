package board

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
	"github.com/mbalazy/pm/internal/version"
)

type view int

const (
	viewBoard view = iota
	viewDetail
	viewArchive
	viewProjectInfo
)

type yankItem struct {
	label string
	value string
}

type linkItem struct {
	name string
	url  string
}

type undoAction struct {
	kind string        // "move" | "done" | "archive" | "delete"
	task *storage.Task // snapshot before the action
}

func snapshotTask(t *storage.Task) *storage.Task {
	cp := *t
	cp.Meta = t.Meta
	if t.Meta.Links != nil {
		cp.Meta.Links = make(map[string]string)
		for k, v := range t.Meta.Links {
			cp.Meta.Links[k] = v
		}
	}
	if t.Meta.Tags != nil {
		cp.Meta.Tags = make([]string, len(t.Meta.Tags))
		copy(cp.Meta.Tags, t.Meta.Tags)
	}
	return &cp
}

type Model struct {
	store         *storage.Store
	tasks         []*storage.Task
	projects      []string
	statuses      []storage.TaskStatus
	activeProject int // 0 = all, 1+ = specific project
	activeCol     int
	cursors       []int
	width         int
	height        int
	currentView   view
	previousView  view
	detailViewport viewport.Model
	searchInput   textinput.Model
	searchQuery   string
	searching     bool
	showHelp      bool
	adding        bool
	addStep       int // 0=title, 1=ID
	addInput      textinput.Model
	addTitle      string
	err           error

	// confirmation
	confirmAction string // "" | "done" | "delete"
	confirmTaskID string

	// archive
	archiveCursor int

	// project counts for tabs
	projectCounts map[string]int

	// yank menu
	yankMenu   bool
	yankItems  []yankItem
	yankCursor int

	// links menu
	linksMenu   bool
	linkItems   []linkItem
	linksCursor int

	// toast notification
	toastMsg    string
	toastExpiry time.Time

	// undo
	lastUndo *undoAction

	// help search
	helpSearch bool
	helpFilter string
	helpInput  textinput.Model
}

func New(store *storage.Store, filterProject string) Model {
	m := Model{
		store:         store,
		width:         80,
		height:        24,
		projectCounts: make(map[string]int),
	}

	projects, _ := store.ListProjects()
	m.projects = append([]string{"all"}, projects...)

	if filterProject != "" {
		for i, p := range m.projects {
			if p == filterProject {
				m.activeProject = i
				break
			}
		}
	}

	m.loadStatuses()
	m.cursors = make([]int, len(m.statuses))
	m.reload()

	ti := textinput.New()
	ti.Prompt = "/ "
	ti.CharLimit = 50
	m.searchInput = ti

	ai := textinput.New()
	ai.Prompt = "Title: "
	ai.CharLimit = 100
	m.addInput = ai

	hi := textinput.New()
	hi.Prompt = "/ "
	hi.CharLimit = 30
	m.helpInput = hi

	return m
}

func (m *Model) loadStatuses() {
	if m.activeProject == 0 {
		m.statuses = m.store.GetAllStatuses()
	} else {
		slug := m.projects[m.activeProject]
		m.statuses = m.store.GetProjectStatuses(slug)
	}
}

func (m *Model) loadTasks() {
	if m.activeProject == 0 {
		m.tasks, m.err = m.store.GetAllTasks()
	} else {
		slug := m.projects[m.activeProject]
		m.tasks, m.err = m.store.GetTasks(slug)
	}
}

func (m *Model) reload() {
	m.loadTasks()
	m.loadProjectCounts()
	m.fixCursors()
}

func (m *Model) loadProjectCounts() {
	m.projectCounts = make(map[string]int)
	all, _ := m.store.GetAllTasks()
	total := 0
	for _, t := range all {
		if t.Meta.Status == storage.StatusArchived {
			continue
		}
		m.projectCounts[t.Project]++
		total++
	}
	m.projectCounts["all"] = total
}

func (m Model) filteredTasks(status storage.TaskStatus) []*storage.Task {
	var result []*storage.Task
	q := strings.ToLower(m.searchQuery)
	for _, t := range m.tasks {
		if t.Meta.Status == storage.StatusArchived {
			continue
		}
		if t.Meta.Status != status {
			continue
		}
		if q != "" {
			title := strings.ToLower(t.Meta.Title)
			id := strings.ToLower(t.Meta.ID)
			if !strings.Contains(title, q) && !strings.Contains(id, q) {
				continue
			}
		}
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Meta.Updated > result[j].Meta.Updated
	})
	return result
}

func (m Model) archivedTasks() []*storage.Task {
	var result []*storage.Task
	q := strings.ToLower(m.searchQuery)
	for _, t := range m.tasks {
		if t.Meta.Status != storage.StatusArchived {
			continue
		}
		if q != "" {
			title := strings.ToLower(t.Meta.Title)
			id := strings.ToLower(t.Meta.ID)
			if !strings.Contains(title, q) && !strings.Contains(id, q) {
				continue
			}
		}
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Meta.Updated > result[j].Meta.Updated
	})
	return result
}

func (m Model) selectedArchiveTask() *storage.Task {
	tasks := m.archivedTasks()
	if len(tasks) == 0 {
		return nil
	}
	idx := m.archiveCursor
	if idx >= len(tasks) {
		idx = len(tasks) - 1
	}
	return tasks[idx]
}

func (m Model) columnTasks(col int) []*storage.Task {
	if col < 0 || col >= len(m.statuses) {
		return nil
	}
	return m.filteredTasks(m.statuses[col])
}

func (m Model) selectedTask() *storage.Task {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) == 0 {
		return nil
	}
	idx := m.cursors[m.activeCol]
	if idx >= len(tasks) {
		idx = len(tasks) - 1
	}
	return tasks[idx]
}

type tickMsg time.Time

const refreshInterval = 2 * time.Second

func doTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Init() tea.Cmd {
	return doTick()
}

type reloadMsg struct{}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.detailViewport.Width = msg.Width
		m.detailViewport.Height = msg.Height - 2
		return m, nil

	case tickMsg:
		m.reload()
		if m.currentView == viewArchive {
			tasks := m.archivedTasks()
			if m.archiveCursor >= len(tasks) && len(tasks) > 0 {
				m.archiveCursor = len(tasks) - 1
			}
		}
		return m, doTick()

	case reloadMsg:
		m.reload()
		return m, doTick()

	case tea.KeyMsg:
		if m.currentView == viewDetail || m.currentView == viewProjectInfo {
			return m.updateDetail(msg)
		}
		if m.yankMenu {
			return m.updateYankMenu(msg)
		}
		if m.linksMenu {
			return m.updateLinksMenu(msg)
		}
		if m.currentView == viewArchive {
			return m.updateArchive(msg)
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
		case "delete":
			isConfirmKey = key.Matches(msg, common.Keys.Delete)
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
		if m.activeCol > 0 {
			m.activeCol--
		}

	case key.Matches(msg, common.Keys.Right):
		if m.activeCol < len(m.statuses)-1 {
			m.activeCol++
		}

	case key.Matches(msg, common.Keys.Up):
		if m.cursors[m.activeCol] > 0 {
			m.cursors[m.activeCol]--
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.columnTasks(m.activeCol)
		if m.cursors[m.activeCol] < len(tasks)-1 {
			m.cursors[m.activeCol]++
		}

	case key.Matches(msg, common.Keys.JumpTop):
		m.cursors[m.activeCol] = 0

	case key.Matches(msg, common.Keys.JumpBottom):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = len(tasks) - 1
		}

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = (m.activeProject + 1) % len(m.projects)
		m.loadStatuses()
		m.reload()
		m.cursors = make([]int, len(m.statuses))
		if m.activeCol >= len(m.statuses) {
			m.activeCol = 0
		}

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = (m.activeProject - 1 + len(m.projects)) % len(m.projects)
		m.loadStatuses()
		m.reload()
		m.cursors = make([]int, len(m.statuses))
		if m.activeCol >= len(m.statuses) {
			m.activeCol = 0
		}

	case key.Matches(msg, common.Keys.MoveBack):
		t := m.selectedTask()
		if t != nil {
			m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
			idx := m.statusIndex(t.Meta.Status)
			if idx > 0 {
				t.Meta.Status = m.statuses[idx-1]
			} else {
				t.Meta.Status = m.statuses[len(m.statuses)-1]
			}
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.reload()
		}

	case key.Matches(msg, common.Keys.Move):
		t := m.selectedTask()
		if t != nil {
			m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
			idx := m.statusIndex(t.Meta.Status)
			t.Meta.Status = m.statuses[(idx+1)%len(m.statuses)]
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.reload()
		}

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open):
		t := m.selectedTask()
		if t != nil {
			m.previousView = viewBoard
			m.currentView = viewDetail
			m.detailViewport = viewport.New(m.width, m.height-2)
			m.detailViewport.SetContent(renderTaskDetail(t, m.width))
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
			// confirmed
			m.lastUndo = &undoAction{kind: "done", task: snapshotTask(t)}
			t.Meta.Status = m.statuses[len(m.statuses)-1]
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
			m.reload()
		} else {
			m.confirmAction = "done"
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
		if t.Meta.Branch != "" {
			m.copyToClipboard(t.Meta.Branch)
		} else {
			m.toastMsg = "no branch set"
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
		if len(t.Meta.Links) == 1 {
			for _, url := range t.Meta.Links {
				exec.Command("open", url).Start()
			}
		} else {
			m.linkItems = nil
			m.linksCursor = 0
			for name, url := range t.Meta.Links {
				m.linkItems = append(m.linkItems, linkItem{name, url})
			}
			m.linksMenu = true
		}

	case key.Matches(msg, common.Keys.Archive):
		t := m.selectedTask()
		if t != nil {
			m.lastUndo = &undoAction{kind: "archive", task: snapshotTask(t)}
			t.Meta.Status = storage.StatusArchived
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.reload()
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
			m.detailViewport = viewport.New(m.width, m.height-2)
			m.detailViewport.SetContent(renderProjectInfo(proj, slug, m.width))
		}

	case key.Matches(msg, common.Keys.Undo):
		if m.lastUndo == nil {
			m.toastMsg = "nothing to undo"
			m.toastExpiry = time.Now().Add(2 * time.Second)
			break
		}
		u := m.lastUndo
		storage.WriteTask(u.task)
		m.lastUndo = nil
		m.toastMsg = "undone: " + u.kind
		m.toastExpiry = time.Now().Add(2 * time.Second)
		m.reload()

	case key.Matches(msg, common.Keys.Help):
		m.showHelp = true

	case key.Matches(msg, common.Keys.Add):
		if m.activeProject == 0 {
			if len(m.projects) > 1 {
				m.activeProject = 1
				m.loadStatuses()
				m.reload()
				m.cursors = make([]int, len(m.statuses))
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

	case key.Matches(msg, common.Keys.Search):
		m.searching = true
		m.searchInput.SetValue(m.searchQuery)
		m.searchInput.Focus()
		return m, m.searchInput.Cursor.BlinkCmd()
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
		now := time.Now().Format("2006-01-02")
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
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open):
		m.currentView = m.previousView
		m.reload()
		return m, nil
	}
	var cmd tea.Cmd
	m.detailViewport, cmd = m.detailViewport.Update(msg)
	return m, cmd
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
		return m, tea.Quit

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
			t.Meta.Status = statuses[0]
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.reload()
			tasks := m.archivedTasks()
			if m.archiveCursor >= len(tasks) && len(tasks) > 0 {
				m.archiveCursor = len(tasks) - 1
			}
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
			tasks := m.archivedTasks()
			if m.archiveCursor >= len(tasks) && len(tasks) > 0 {
				m.archiveCursor = len(tasks) - 1
			}
		} else {
			m.confirmAction = "delete"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open):
		t := m.selectedArchiveTask()
		if t != nil {
			m.previousView = viewArchive
			m.currentView = viewDetail
			m.detailViewport = viewport.New(m.width, m.height-2)
			m.detailViewport.SetContent(renderTaskDetail(t, m.width))
		}

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = (m.activeProject + 1) % len(m.projects)
		m.loadStatuses()
		m.reload()
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = (m.activeProject - 1 + len(m.projects)) % len(m.projects)
		m.loadStatuses()
		m.reload()
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.Undo):
		if m.lastUndo == nil {
			m.toastMsg = "nothing to undo"
			m.toastExpiry = time.Now().Add(2 * time.Second)
			break
		}
		u := m.lastUndo
		storage.WriteTask(u.task)
		m.lastUndo = nil
		m.toastMsg = "undone: " + u.kind
		m.toastExpiry = time.Now().Add(2 * time.Second)
		m.reload()
		tasks := m.archivedTasks()
		if m.archiveCursor >= len(tasks) && len(tasks) > 0 {
			m.archiveCursor = len(tasks) - 1
		}

	case key.Matches(msg, common.Keys.Search):
		m.searching = true
		m.searchInput.SetValue(m.searchQuery)
		m.searchInput.Focus()
		return m, m.searchInput.Cursor.BlinkCmd()
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
		m.linksMenu = false
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
		tasks := m.columnTasks(i)
		if m.cursors[i] >= len(tasks) && len(tasks) > 0 {
			m.cursors[i] = len(tasks) - 1
		}
	}
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

func renderTaskDetail(t *storage.Task, termWidth int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s", t.Meta.Title)
	if t.Meta.ID != "" {
		fmt.Fprintf(&sb, " (#%s)", t.Meta.ID)
	}
	fmt.Fprintln(&sb)
	fmt.Fprintf(&sb, "\n**Status:** %s | **Project:** %s | **Updated:** %s\n", t.Meta.Status, t.Project, t.Meta.Updated)
	if t.Meta.Branch != "" {
		fmt.Fprintf(&sb, "\n**Branch:** `%s`\n", t.Meta.Branch)
	}
	if len(t.Meta.Links) > 0 {
		var parts []string
		for name, url := range t.Meta.Links {
			parts = append(parts, fmt.Sprintf("[%s](%s)", name, url))
		}
		fmt.Fprintf(&sb, "\n**Links:** %s\n", strings.Join(parts, " | "))
	}
	if len(t.Meta.Tags) > 0 {
		fmt.Fprintf(&sb, "\n**Tags:** %s\n", strings.Join(t.Meta.Tags, ", "))
	}
	if t.Body != "" {
		fmt.Fprintf(&sb, "\n---\n\n%s\n", t.Body)
	}

	contentWidth := termWidth - 4
	if contentWidth > 100 {
		contentWidth = 100
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(contentWidth),
	)
	if err != nil {
		return sb.String()
	}
	rendered, err := r.Render(sb.String())
	if err != nil {
		return sb.String()
	}

	pad := (termWidth - contentWidth) / 2
	if pad < 0 {
		pad = 0
	}
	style := lipgloss.NewStyle().PaddingLeft(pad)
	return style.Render(rendered)
}

func renderProjectInfo(proj *storage.Project, slug string, termWidth int) string {
	var sb strings.Builder
	name := proj.Name
	if name == "" {
		name = slug
	}
	fmt.Fprintf(&sb, "# %s\n", name)
	if proj.Stack != "" {
		fmt.Fprintf(&sb, "\n**Stack:** %s\n", proj.Stack)
	}
	if proj.Notes != "" {
		fmt.Fprintf(&sb, "\n**Notes:** %s\n", proj.Notes)
	}
	if proj.Repo != "" {
		fmt.Fprintf(&sb, "\n**Repo:** %s\n", proj.Repo)
	}
	if proj.Path != "" {
		fmt.Fprintf(&sb, "\n**Path:** %s\n", proj.Path)
	}
	if len(proj.Tags) > 0 {
		fmt.Fprintf(&sb, "\n**Tags:** %s\n", strings.Join(proj.Tags, ", "))
	}
	statuses := proj.GetStatuses()
	var statusNames []string
	for _, s := range statuses {
		statusNames = append(statusNames, string(s))
	}
	fmt.Fprintf(&sb, "\n**Statuses:** %s\n", strings.Join(statusNames, " → "))
	if len(proj.Links) > 0 {
		var parts []string
		for name, url := range proj.Links {
			parts = append(parts, fmt.Sprintf("[%s](%s)", name, url))
		}
		fmt.Fprintf(&sb, "\n**Links:** %s\n", strings.Join(parts, " | "))
	}

	contentWidth := termWidth - 4
	if contentWidth > 100 {
		contentWidth = 100
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(contentWidth),
	)
	if err != nil {
		return sb.String()
	}
	rendered, err := r.Render(sb.String())
	if err != nil {
		return sb.String()
	}

	pad := (termWidth - contentWidth) / 2
	if pad < 0 {
		pad = 0
	}
	style := lipgloss.NewStyle().PaddingLeft(pad)
	return style.Render(rendered)
}

func (m Model) View() string {
	if m.currentView == viewDetail || m.currentView == viewProjectInfo {
		return m.viewDetail()
	}
	if m.yankMenu {
		return m.viewYankMenu()
	}
	if m.linksMenu {
		return m.viewLinksMenu()
	}
	if m.showHelp {
		return m.viewHelp()
	}
	if m.currentView == viewArchive {
		return m.viewArchive()
	}
	return m.viewBoard()
}

func (m Model) viewDetail() string {
	var sb strings.Builder
	sb.WriteString(m.detailViewport.View())
	sb.WriteString("\n")
	pct := fmt.Sprintf("%3.f%%", m.detailViewport.ScrollPercent()*100)
	sb.WriteString(helpStyle.Render("o/esc/q: back  ↑/↓/j/k scroll  " + pct))
	return sb.String()
}

func (m Model) viewHelp() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Keyboard Shortcuts")
	allKeys := []struct{ key, desc string }{
		{"↑ / k", "Move up"},
		{"↓ / j", "Move down"},
		{"← / h", "Previous column"},
		{"→ / l", "Next column"},
		{"g", "Jump to top"},
		{"G", "Jump to bottom"},
		{"m", "Move task forward"},
		{"M", "Move task back"},
		{"d", "Mark done (confirm)"},
		{"x", "Delete task (confirm)"},
		{"A", "Archive task"},
		{"a", "Add new task"},
		{"e", "Edit in $EDITOR"},
		{"y", "Yank branch"},
		{"Y", "Yank menu"},
		{"L", "Open links"},
		{"i", "Project info"},
		{"o / Enter", "Task detail"},
		{"/ ", "Search tasks"},
		{"Ctrl+a", "Archive view"},
		{"Tab", "Next project"},
		{"S-Tab", "Previous project"},
		{"u", "Undo last action"},
		{"?", "This help"},
		{"q", "Quit (confirm)"},
	}

	// filter keys if search active
	var keys []struct{ key, desc string }
	if m.helpFilter != "" {
		q := strings.ToLower(m.helpFilter)
		for _, k := range allKeys {
			if strings.Contains(strings.ToLower(k.key), q) || strings.Contains(strings.ToLower(k.desc), q) {
				keys = append(keys, k)
			}
		}
	} else {
		keys = allKeys
	}

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	if len(keys) == 0 {
		lines = append(lines, helpStyle.Render("  no matches"))
	} else {
		for _, k := range keys {
			keyStyle := lipgloss.NewStyle().Bold(true).Foreground(special).Width(12)
			lines = append(lines, keyStyle.Render(k.key)+"  "+k.desc)
		}
	}
	lines = append(lines, "")

	if m.helpSearch {
		lines = append(lines, m.helpInput.View())
	} else if m.helpFilter != "" {
		lines = append(lines, helpStyle.Render(fmt.Sprintf("filter: %q  / edit  esc clear", m.helpFilter)))
	} else {
		lines = append(lines, helpStyle.Render("/ filter  esc close"))
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewYankMenu() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Yank to clipboard")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for i, item := range m.yankItems {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
		if i == m.yankCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		display := item.value
		if len(display) > 50 {
			display = display[:47] + "..."
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%s: %s", prefix, item.label, display)))
	}
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("enter copy  esc back"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewLinksMenu() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Open link")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for i, item := range m.linkItems {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
		if i == m.linksCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		display := item.url
		if len(display) > 50 {
			display = display[:47] + "..."
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%s: %s", prefix, item.name, display)))
	}
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("enter open  esc back"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) viewArchive() string {
	var sb strings.Builder

	sb.WriteString(titleStyle.Render("pm archive"))
	sb.WriteString("\n")

	// project tabs
	var tabs []string
	for i, p := range m.projects {
		name := p
		if i == 0 {
			name = "ALL"
		}
		if i == m.activeProject {
			tabs = append(tabs, activeTabStyle.Render("["+name+"]"))
		} else {
			tabs = append(tabs, tabStyle.Render(name))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	sb.WriteString("\n\n")

	tasks := m.archivedTasks()
	if len(tasks) == 0 {
		sb.WriteString(helpStyle.Render("  No archived tasks."))
		sb.WriteString("\n")
	} else {
		listHeight := m.height - 10
		if listHeight < 3 {
			listHeight = 3
		}
		cardWidth := m.width - 8
		if cardWidth > 100 {
			cardWidth = 100
		}
		for i, t := range tasks {
			if i >= listHeight {
				break
			}
			isSelected := i == m.archiveCursor
			card := renderCard(t, cardWidth, isSelected)
			sb.WriteString("  " + card)
			sb.WriteString("\n")
		}
	}

	// confirmation prompt
	if m.confirmAction != "" {
		sb.WriteString("\n")
		prompt := fmt.Sprintf("  press x again to delete")
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"}).Render(prompt))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	help := helpStyle.Render("archived: " + fmt.Sprint(len(tasks)) + "  ↑/↓ navigate  r restore  u undo  x delete  o detail  esc back")
	sb.WriteString(help)

	return sb.String()
}

func (m Model) viewBoard() string {
	var sb strings.Builder

	// title
	sb.WriteString(titleStyle.Render("pm board"))
	sb.WriteString("\n")

	// project tabs with counts
	var tabs []string
	for i, p := range m.projects {
		name := p
		if i == 0 {
			name = "ALL"
		}
		count := m.projectCounts[p]
		label := fmt.Sprintf("%s (%d)", name, count)
		if i == m.activeProject {
			tabs = append(tabs, activeTabStyle.Render("["+label+"]"))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
	}
	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	sb.WriteString("\n\n")

	// columns
	numCols := len(m.statuses)
	if numCols == 0 {
		numCols = 1
	}
	colWidth := (m.width - 8) / numCols
	if colWidth < 20 {
		colWidth = 20
	}
	maxCardHeight := m.height - 10
	if maxCardHeight < 5 {
		maxCardHeight = 5
	}

	var cols []string
	for i, status := range m.statuses {
		tasks := m.filteredTasks(status)
		isActive := i == m.activeCol

		header := strings.ToUpper(string(status))
		var content strings.Builder
		content.WriteString(columnHeaderStyle.Render(fmt.Sprintf("%s (%d)", header, len(tasks))))
		content.WriteString("\n")

		for j, t := range tasks {
			isSelected := isActive && j == m.cursors[i]
			card := renderCard(t, colWidth-6, isSelected)
			content.WriteString(card)
			content.WriteString("\n")
		}

		style := columnStyle
		if isActive {
			style = activeColumnStyle
		}
		col := style.Width(colWidth).Height(maxCardHeight).Render(content.String())
		cols = append(cols, col)
	}

	sb.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cols...))
	sb.WriteString("\n")

	// search bar / add input
	if m.adding {
		sb.WriteString(m.addInput.View())
		sb.WriteString("\n")
	} else if m.searching {
		sb.WriteString(m.searchInput.View())
		sb.WriteString("\n")
	} else if m.searchQuery != "" {
		sb.WriteString(helpStyle.Render(fmt.Sprintf("filter: %q (/ to edit, esc to clear)", m.searchQuery)))
		sb.WriteString("\n")
	}

	// confirmation prompt
	if m.confirmAction != "" {
		var prompt string
		switch m.confirmAction {
		case "done":
			prompt = "  press d again to mark done"
		case "delete":
			prompt = "  press x again to delete"
		case "quit":
			prompt = "  press q again to quit"
		}
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"}).Render(prompt))
		sb.WriteString("\n")
	}

	// status bar
	ver := helpStyle.Render("pm " + version.Version)
	help := helpStyle.Render("g/G jump  m/M move  d done  x del  A archive  u undo  a add  y/Y yank  L links  i info  e edit  / search  C-a archived  ? help  q quit")
	gap := m.width - lipgloss.Width(ver) - lipgloss.Width(help)
	if gap < 1 {
		gap = 1
	}
	sb.WriteString(ver + strings.Repeat(" ", gap) + help)

	result := sb.String()

	// Overlay toast in top-right corner if active
	if m.toastMsg != "" && time.Now().Before(m.toastExpiry) {
		toast := toastStyle.Render(" " + m.toastMsg + " ")
		// Place toast at the end of the first line
		lines := strings.SplitN(result, "\n", 2)
		if len(lines) >= 1 {
			titleWidth := lipgloss.Width(lines[0])
			toastWidth := lipgloss.Width(toast)
			padding := m.width - titleWidth - toastWidth
			if padding > 0 {
				lines[0] = lines[0] + strings.Repeat(" ", padding) + toast
			} else {
				lines[0] = lines[0] + " " + toast
			}
			if len(lines) > 1 {
				result = lines[0] + "\n" + lines[1]
			} else {
				result = lines[0]
			}
		}
	}

	return result
}

func renderCard(t *storage.Task, width int, selected bool) string {
	style := cardStyle
	if selected {
		style = activeCardStyle
	}
	style = style.Width(width)

	var lines []string

	title := t.Meta.Title
	if t.Meta.ID != "" {
		title = "#" + t.Meta.ID + " " + title
	}
	// truncate title if too long
	if len(title) > width-2 {
		title = title[:width-5] + "..."
	}
	lines = append(lines, cardTitleStyle.Render(title))
	lines = append(lines, cardProjectStyle.Render(t.Project))

	if len(t.Meta.Tags) > 0 {
		lines = append(lines, cardTagStyle.Render(strings.Join(t.Meta.Tags, ", ")))
	}

	return style.Render(strings.Join(lines, "\n"))
}
