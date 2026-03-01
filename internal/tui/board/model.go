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

// ProcessStart is set in main's init() to measure startup time.
var ProcessStart time.Time

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

type colVisItem struct {
	status  storage.TaskStatus
	visible bool
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
	detailTask     *storage.Task
	infoProject    *storage.Project
	infoSlug       string
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

	// column scroll offsets (first visible card index per column)
	scrollOffsets []int

	// undo
	lastUndo *undoAction

	// help search
	helpSearch bool
	helpFilter string
	helpInput  textinput.Model

	// column visibility
	colVisMenu     bool
	colVisItems    []colVisItem
	colVisCursor   int
	hiddenStatuses map[storage.TaskStatus]bool

	// zoom
	zoomed bool

	startupDuration time.Duration
}

func New(store *storage.Store, filterProject string) Model {
	m := Model{
		store:          store,
		width:          80,
		height:         24,
		projectCounts:  make(map[string]int),
		hiddenStatuses: make(map[storage.TaskStatus]bool),
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

	if !ProcessStart.IsZero() {
		m.startupDuration = time.Since(ProcessStart)
	}

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
	m.loadStatuses()
	m.applyColumnVisibility()
	n := len(m.statuses)
	if len(m.cursors) != n {
		old := m.cursors
		m.cursors = make([]int, n)
		copy(m.cursors, old)
		oldOff := m.scrollOffsets
		m.scrollOffsets = make([]int, n)
		copy(m.scrollOffsets, oldOff)
	}
	if m.activeCol >= n && n > 0 {
		m.activeCol = n - 1
	} else if n == 0 {
		m.activeCol = 0
	}
	m.loadProjectCounts()
	m.fixCursors()
}

func (m *Model) applyColumnVisibility() {
	// Auto-hide waiting if empty and not explicitly shown
	if _, explicit := m.hiddenStatuses[storage.StatusWaiting]; !explicit {
		hasWaiting := false
		for _, t := range m.tasks {
			if t.Meta.Status == storage.StatusWaiting {
				hasWaiting = true
				break
			}
		}
		if !hasWaiting {
			m.hiddenStatuses[storage.StatusWaiting] = true
		} else {
			delete(m.hiddenStatuses, storage.StatusWaiting)
		}
	}

	filtered := m.statuses[:0]
	for _, s := range m.statuses {
		if !m.hiddenStatuses[s] {
			filtered = append(filtered, s)
		}
	}
	m.statuses = filtered
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
		if m.colVisMenu {
			return m.updateColVisMenu(msg)
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
		case "waiting":
			isConfirmKey = key.Matches(msg, common.Keys.Waiting)
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
		if len(m.statuses) > 0 {
			m.activeCol = (m.activeCol - 1 + len(m.statuses)) % len(m.statuses)
		}

	case key.Matches(msg, common.Keys.Right):
		if len(m.statuses) > 0 {
			m.activeCol = (m.activeCol + 1) % len(m.statuses)
		}

	case key.Matches(msg, common.Keys.Up):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = (m.cursors[m.activeCol] - 1 + len(tasks)) % len(tasks)
			m.fixScrollOffsets()
		}

	case key.Matches(msg, common.Keys.Down):
		tasks := m.columnTasks(m.activeCol)
		if len(tasks) > 0 {
			m.cursors[m.activeCol] = (m.cursors[m.activeCol] + 1) % len(tasks)
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

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = (m.activeProject + 1) % len(m.projects)
		m.reload()

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = (m.activeProject - 1 + len(m.projects)) % len(m.projects)
		m.reload()

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

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		t := m.selectedTask()
		if t != nil {
			m.previousView = viewBoard
			m.currentView = viewDetail
			m.detailTask = t
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
			t.Meta.Brief = ""
			storage.WriteTask(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
			m.reload()
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
			m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
			t.Meta.Status = storage.StatusWaiting
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.confirmAction = ""
			m.confirmTaskID = ""
			m.reload()
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
		if t != nil {
			m.lastUndo = &undoAction{kind: "archive", task: snapshotTask(t)}
			t.Meta.Status = storage.StatusArchived
			t.Meta.Updated = time.Now().Format("2006-01-02")
			t.Meta.Brief = ""
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
			m.infoProject = proj
			m.infoSlug = slug
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

	case key.Matches(msg, common.Keys.Zoom):
		m.zoomed = !m.zoomed

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
	// Overlay menus intercept input first
	if m.yankMenu {
		return m.updateYankMenu(msg)
	}
	if m.linksMenu {
		return m.updateLinksMenu(msg)
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
		}
		var cmd tea.Cmd
		m.detailViewport, cmd = m.detailViewport.Update(msg)
		return m, cmd
	}

	t := m.detailTask
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		m.currentView = m.previousView
		m.reload()
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
			if t.Meta.Brief != "" {
				m.yankItems = append(m.yankItems, yankItem{"brief", t.Meta.Brief})
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
			m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
			idx := m.statusIndex(t.Meta.Status)
			t.Meta.Status = m.statuses[(idx+1)%len(m.statuses)]
			t.Meta.Updated = time.Now().Format("2006-01-02")
			storage.WriteTask(t)
			m.currentView = m.previousView
			m.reload()
			return m, nil
		}

	case key.Matches(msg, common.Keys.MoveBack):
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
			m.currentView = m.previousView
			m.reload()
			return m, nil
		}

	case key.Matches(msg, common.Keys.Done):
		if t != nil {
			if m.confirmAction == "done" && m.confirmTaskID == t.Meta.ID {
				m.lastUndo = &undoAction{kind: "done", task: snapshotTask(t)}
				t.Meta.Status = m.statuses[len(m.statuses)-1]
				t.Meta.Updated = time.Now().Format("2006-01-02")
				t.Meta.Brief = ""
				storage.WriteTask(t)
				m.confirmAction = ""
				m.confirmTaskID = ""
				m.currentView = m.previousView
				m.reload()
				return m, nil
			}
			m.confirmAction = "done"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Waiting):
		if t != nil {
			if m.confirmAction == "waiting" && m.confirmTaskID == t.Meta.ID {
				m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
				t.Meta.Status = storage.StatusWaiting
				t.Meta.Updated = time.Now().Format("2006-01-02")
				storage.WriteTask(t)
				m.confirmAction = ""
				m.confirmTaskID = ""
				m.currentView = m.previousView
				m.reload()
				return m, nil
			}
			m.confirmAction = "waiting"
			m.confirmTaskID = t.Meta.ID
		}

	case key.Matches(msg, common.Keys.Archive):
		if t != nil {
			m.lastUndo = &undoAction{kind: "archive", task: snapshotTask(t)}
			t.Meta.Status = storage.StatusArchived
			t.Meta.Updated = time.Now().Format("2006-01-02")
			t.Meta.Brief = ""
			storage.WriteTask(t)
			m.currentView = m.previousView
			m.reload()
			return m, nil
		}

	default:
		var cmd tea.Cmd
		m.detailViewport, cmd = m.detailViewport.Update(msg)
		return m, cmd
	}
	return m, nil
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

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open), key.Matches(msg, common.Keys.Space):
		t := m.selectedArchiveTask()
		if t != nil {
			m.previousView = viewArchive
			m.currentView = viewDetail
			m.detailTask = t
			m.detailViewport = viewport.New(m.width, m.height-2)
			m.detailViewport.SetContent(renderTaskDetail(t, m.width))
		}

	case key.Matches(msg, common.Keys.Tab):
		m.activeProject = (m.activeProject + 1) % len(m.projects)
		m.reload()
		m.archiveCursor = 0

	case key.Matches(msg, common.Keys.ShiftTab):
		m.activeProject = (m.activeProject - 1 + len(m.projects)) % len(m.projects)
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
		tasks := m.columnTasks(i)
		if m.cursors[i] >= len(tasks) && len(tasks) > 0 {
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
func (m *Model) fixScrollOffsets() {
	maxH := m.height - 10
	if maxH < 5 {
		maxH = 5
	}
	for i := range m.statuses {
		tasks := m.columnTasks(i)
		cursor := m.cursors[i]
		if i >= len(m.scrollOffsets) {
			continue
		}

		// Scroll up if cursor is above the visible window
		if cursor < m.scrollOffsets[i] {
			m.scrollOffsets[i] = cursor
		}

		// Scroll down if cursor is below the visible window
		// Calculate how many cards fit from scrollOffset
		heightFn := cardHeight
		if m.zoomed {
			heightFn = zoomCardHeight
		}
		usedH := 0
		lastVisible := m.scrollOffsets[i]
		for j := m.scrollOffsets[i]; j < len(tasks); j++ {
			h := heightFn(tasks[j])
			if usedH+h > maxH && j > m.scrollOffsets[i] {
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
				h := heightFn(tasks[start])
				if usedH+h > maxH && start < cursor {
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
	if t.Meta.Brief != "" {
		fmt.Fprintf(&sb, "\n**Brief:**\n%s\n", t.Meta.Brief)
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
	if m.colVisMenu {
		return m.viewColVisMenu()
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
	if m.yankMenu {
		return m.viewYankMenu()
	}
	if m.linksMenu {
		return m.viewLinksMenu()
	}
	var sb strings.Builder
	sb.WriteString(m.detailViewport.View())
	sb.WriteString("\n")
	pct := fmt.Sprintf("%3.f%%", m.detailViewport.ScrollPercent()*100)
	help := "o/q: back  e: edit  m/w/d/A: move/wait/done/archive  y/Y: yank  L: links  " + pct
	if m.currentView == viewProjectInfo {
		help = "o/esc/q: back  ↑/↓/j/k scroll  y/Y: yank  L: links  " + pct
	}
	if m.confirmAction == "done" {
		help = "press d again to confirm done  " + pct
	}
	if m.confirmAction == "waiting" {
		help = "press w again to mark waiting  " + pct
	}
	sb.WriteString(helpStyle.Render(help))
	return m.applyToast(sb.String())
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
		{"Ctrl+d", "Half page down"},
		{"Ctrl+u", "Half page up"},
		{"m", "Move task forward"},
		{"M", "Move task back"},
		{"w", "Mark waiting (confirm)"},
		{"d", "Mark done (confirm)"},
		{"x", "Delete task (confirm)"},
		{"A", "Archive task"},
		{"a", "Add new task"},
		{"e", "Edit in $EDITOR"},
		{"y", "Yank ID"},
		{"Y", "Yank menu"},
		{"L", "Open links"},
		{"v", "Toggle columns"},
		{"i", "Project info"},
		{"o / Enter", "Task detail"},
		{"/ ", "Search tasks"},
		{";", "Zoom toggle"},
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
		mid := (len(keys) + 1) / 2
		leftKeys := keys[:mid]
		rightKeys := keys[mid:]
		var leftLines, rightLines []string
		keyStyle := lipgloss.NewStyle().Bold(true).Foreground(special).Width(12)
		for _, k := range leftKeys {
			leftLines = append(leftLines, keyStyle.Render(k.key)+"  "+k.desc)
		}
		for _, k := range rightKeys {
			rightLines = append(rightLines, keyStyle.Render(k.key)+"  "+k.desc)
		}
		leftCol := strings.Join(leftLines, "\n")
		rightCol := strings.Join(rightLines, "\n")
		cols := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, "    ", rightCol)
		lines = append(lines, cols)
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

func (m Model) viewColVisMenu() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render("Visible columns")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	for i, item := range m.colVisItems {
		check := "[ ]"
		if item.visible {
			check = "[x]"
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
		prefix := "  "
		if i == m.colVisCursor {
			prefix = "> "
			style = lipgloss.NewStyle().Bold(true).Foreground(special)
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%s %s", prefix, check, string(item.status))))
	}
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("enter toggle  esc back"))

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
			sb.WriteString(lipgloss.NewStyle().PaddingLeft(2).Render(card))
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

	// In zoom mode, show only the active column at full width
	var visibleStatuses []storage.TaskStatus
	var visibleIndices []int
	if m.zoomed && len(m.statuses) > 0 {
		visibleStatuses = []storage.TaskStatus{m.statuses[m.activeCol]}
		visibleIndices = []int{m.activeCol}
		numCols = 1
	} else {
		visibleStatuses = m.statuses
		visibleIndices = make([]int, len(m.statuses))
		for i := range m.statuses {
			visibleIndices[i] = i
		}
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
	for vi, status := range visibleStatuses {
		i := visibleIndices[vi]
		tasks := m.filteredTasks(status)
		isActive := i == m.activeCol

		header := strings.ToUpper(string(status))
		var content strings.Builder
		if m.zoomed {
			content.WriteString(columnHeaderStyle.Render(fmt.Sprintf("[ZOOM] %s (%d)", header, len(tasks))))
		} else {
			content.WriteString(columnHeaderStyle.Render(fmt.Sprintf("%s (%d)", header, len(tasks))))
		}
		content.WriteString("\n")

		// Render only visible cards within scroll window
		offset := 0
		if i < len(m.scrollOffsets) {
			offset = m.scrollOffsets[i]
		}

		if offset > 0 {
			content.WriteString(helpStyle.Render(fmt.Sprintf("  ▲ %d more", offset)))
			content.WriteString("\n")
		}

		usedH := 0
		rendered := 0
		for j := offset; j < len(tasks); j++ {
			var h int
			if m.zoomed {
				h = zoomCardHeight(tasks[j])
			} else {
				h = cardHeight(tasks[j])
			}
			if usedH+h > maxCardHeight && rendered > 0 {
				remaining := len(tasks) - j
				content.WriteString(helpStyle.Render(fmt.Sprintf("  ▼ %d more", remaining)))
				content.WriteString("\n")
				break
			}
			isSelected := isActive && j == m.cursors[i]
			var card string
			if m.zoomed {
				card = renderZoomCard(tasks[j], colWidth-6, isSelected)
			} else {
				card = renderCard(tasks[j], colWidth-6, isSelected)
			}
			content.WriteString(card)
			content.WriteString("\n")
			usedH += h
			rendered++
		}

		style := columnStyle
		if isActive {
			style = activeColumnStyle
		}
		// Pad content so all columns reach the same height
		contentStr := content.String()
		contentLines := strings.Count(contentStr, "\n")
		if contentLines < maxCardHeight {
			contentStr += strings.Repeat("\n", maxCardHeight-contentLines)
		}
		col := style.Width(colWidth).Render(contentStr)
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
		case "waiting":
			prompt = "  press w again to mark waiting"
		case "delete":
			prompt = "  press x again to delete"
		case "quit":
			prompt = "  press q again to quit"
		}
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF6666"}).Render(prompt))
		sb.WriteString("\n")
	}

	// status bar
	statusBar := func() string {
		startup := fmt.Sprintf("%dms", m.startupDuration.Milliseconds())
		ver := helpStyle.Render("pm " + version.Version + " " + startup)
		help := helpStyle.Render("m/M move  w wait  d done  A archive  a add  y/Y yank  L links  e edit  i info  C-a archived")
		gap := m.width - lipgloss.Width(ver) - lipgloss.Width(help)
		if gap < 1 {
			gap = 1
		}
		return ver + strings.Repeat(" ", gap) + help
	}()

	// Pad to fill terminal height so status bar sits at the bottom
	currentHeight := strings.Count(sb.String(), "\n") + 1 // +1 for status bar line
	if pad := m.height - currentHeight - 1; pad > 0 {
		sb.WriteString(strings.Repeat("\n", pad))
	}
	sb.WriteString(statusBar)

	return m.applyToast(sb.String())
}

func (m Model) applyToast(result string) string {
	if m.toastMsg == "" || !time.Now().Before(m.toastExpiry) {
		return result
	}
	toast := toastStyle.Render(" " + m.toastMsg + " ")
	toastWidth := lipgloss.Width(toast)
	lines := strings.Split(result, "\n")
	if len(lines) > 0 {
		first := lines[0]
		firstWidth := lipgloss.Width(first)
		if firstWidth+toastWidth+1 <= m.width {
			gap := m.width - firstWidth - toastWidth
			lines[0] = first + strings.Repeat(" ", gap) + toast
		} else {
			// Overlay: replace end of first line with toast
			// Truncate first line to make room
			target := m.width - toastWidth
			if target < 0 {
				target = 0
			}
			lines[0] = lipgloss.NewStyle().Width(target).Render(first) + toast
		}
		result = strings.Join(lines, "\n")
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

func renderZoomCard(t *storage.Task, width int, selected bool) string {
	style := cardStyle
	if selected {
		style = activeCardStyle
	}
	style = style.Width(width)

	var lines []string

	// Title (full, no truncation - we have space)
	title := t.Meta.Title
	if t.Meta.ID != "" {
		title = "#" + t.Meta.ID + " " + title
	}
	lines = append(lines, cardTitleStyle.Render(title))

	// Project + updated date on same conceptual level
	meta := t.Project
	if t.Meta.Updated != "" {
		meta += "  " + helpStyle.Render(t.Meta.Updated)
	}
	lines = append(lines, cardProjectStyle.Render(meta))

	if len(t.Meta.Tags) > 0 {
		lines = append(lines, cardTagStyle.Render(strings.Join(t.Meta.Tags, ", ")))
	}

	if t.Meta.Branch != "" {
		lines = append(lines, helpStyle.Render("branch: "+t.Meta.Branch))
	}

	if len(t.Meta.Links) > 0 {
		var lk []string
		for name := range t.Meta.Links {
			lk = append(lk, name)
		}
		sort.Strings(lk)
		lines = append(lines, helpStyle.Render("links: "+strings.Join(lk, ", ")))
	}

	if t.Meta.Brief != "" {
		brief := t.Meta.Brief
		maxLen := width * 2 // ~2 lines worth
		if len(brief) > maxLen {
			brief = brief[:maxLen-3] + "..."
		}
		lines = append(lines, "")
		lines = append(lines, helpStyle.Render(brief))
	}

	return style.Render(strings.Join(lines, "\n"))
}

func zoomCardHeight(t *storage.Task) int {
	lines := 2 // title + project
	if len(t.Meta.Tags) > 0 {
		lines++
	}
	if t.Meta.Branch != "" {
		lines++
	}
	if len(t.Meta.Links) > 0 {
		lines++
	}
	if t.Meta.Brief != "" {
		lines += 3 // blank line + up to 2 lines of brief
	}
	return lines + 3 // +2 border, +1 margin
}
