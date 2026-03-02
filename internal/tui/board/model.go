package board

import (
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
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

type claudeMenuItem struct {
	label    string
	kind     string // "here", "tmux", "worktree", "worktree-tmux", "resume", "resume-tmux"
	shortcut string // single key mnemonic
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

	// claude menu
	claudeMenu          bool
	claudeMenuItems     []claudeMenuItem
	claudeMenuCursor    int
	claudeMenuSkipPerms bool

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
		if result[i].Meta.Order != result[j].Meta.Order {
			return result[i].Meta.Order < result[j].Meta.Order
		}
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
