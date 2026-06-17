package board

import (
	"fmt"
	"sort"
	"strconv"
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
	viewFocus
	viewExecutor
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

type launchAgent string

const (
	launchAgentClaude   launchAgent = "claude"
	launchAgentCodex    launchAgent = "codex"
	launchAgentExecutor launchAgent = "executor"
)

func (a launchAgent) label() string {
	switch a {
	case launchAgentCodex:
		return "Codex"
	case launchAgentExecutor:
		return "Executor (pm work/run-epic)"
	default:
		return "Claude Code"
	}
}

type sessionMenuItem struct {
	sessionID string
	summary   string // from sessions-index.json
	msgCount  int
	modified  string // formatted date
	branch    string
	isLatest  bool
}

type pickerItem struct {
	slug         string
	name         string
	stack        string
	taskCount    int
	doingCount   int
	hidden       bool
	path         string
	repo         string
	links        map[string]string
	tags         []string
	statusCounts map[storage.TaskStatus]int
	statuses     []storage.TaskStatus
	lastUpdated  string
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
	store          storage.TaskStore
	tasks          []*storage.Task
	projects       []string
	statuses       []storage.TaskStatus
	activeProject  int // 0 = all, 1+ = specific project
	activeCol      int
	cursors        []int
	width          int
	height         int
	currentView    view
	previousView   view
	detailViewport viewport.Model
	detailTask     *storage.Task
	infoProject    *storage.Project
	infoSlug       string
	searchInput    textinput.Model
	searchQuery    string
	searching      bool
	showHelp       bool
	adding         bool
	addStep        int // 0=title, 1=ID
	addInput       textinput.Model
	addTitle       string
	err            error

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

	// multi-select
	selecting bool
	selected  map[string]bool // task Meta.ID -> true

	// claude menu
	claudeMenu          bool
	claudeMenuItems     []claudeMenuItem
	claudeMenuCursor    int
	claudeMenuSkipPerms bool
	launchAgent         launchAgent
	executorIsTracker   bool // when launchAgent==executor: run-epic vs work (for menu title)

	// executor run-states (live background runs), keyed by task/tracker id;
	// refreshed on tick from <project>/.executor/*.json
	runStates map[string]*storage.RunState

	// executor agent-view (18-7): live transcript of the running worker
	executorViewport   viewport.Model
	executorRunTaskID  string            // task/tracker id whose run we're watching
	executorRunProj    string            // project slug of that run
	executorRun        *storage.RunState // latest run-state for the header
	executorSessions   []execSession     // sub entries that have a worker session, in order
	executorSessionIdx int               // which session is shown
	executorFollow     bool              // auto-scroll to the latest activity
	executorVerbose    bool              // full conversation (untruncated) vs compact feed
	executorPrevView   view              // where to return on esc (kept separate from previousView)

	// project-scope claude launch (no task)
	projectScopeLaunch bool
	projectScopeSlug   string

	// detail search
	detailSearching     bool
	detailSearchInput   textinput.Model
	detailSearchQuery   string
	detailSearchMatches []int // line numbers of matches
	detailSearchIdx     int
	detailPlainContent  string // ANSI-stripped for searching

	// session menu
	sessionMenu      bool
	sessionMenuItems []sessionMenuItem
	sessionCursor    int
	resumeSessionID  string // override for launchClaude resume
	resumeOnly       bool   // when true, claude menu shows only resume options
	forkMode         bool   // when true with resumeOnly, claude menu shows fork options

	startupDuration time.Duration

	// focus plan
	focusCursor int
	focusPlan   storage.FocusPlan
	focusSet    map[string]bool // O(1) lookup for card rendering

	// hidden projects (not shown in tab bar)
	hiddenProjects map[string]bool

	// project picker overlay
	projectPicker bool
	pickerItems   []pickerItem
	pickerCursor  int
	pickerInput   textinput.Model
	pickerFilter  string

	// subtask picker overlay (parent -> child navigation in detail view)
	subtaskPicker bool
	subtaskItems  []*storage.Task
	subtaskCursor int
}

func New(store storage.TaskStore, filterProject string) Model {
	m := Model{
		store:          store,
		width:          80,
		height:         24,
		projectCounts:  make(map[string]int),
		hiddenStatuses: make(map[storage.TaskStatus]bool),
		hiddenProjects: make(map[string]bool),
		selected:       make(map[string]bool),
		focusSet:       make(map[string]bool),
	}

	// load hidden projects from config
	cfg := loadTUIConfig(store.RootDir())
	for _, s := range cfg.HiddenProjects {
		m.hiddenProjects[s] = true
	}

	projects, _ := store.ListActiveProjects()
	m.projects = append([]string{"all"}, applyProjectOrder(projects, cfg.ProjectOrder)...)

	if filterProject != "" {
		for i, p := range m.projects {
			if p == filterProject {
				m.activeProject = i
				break
			}
		}
	}

	m.reload()
	m.loadFocusPlan()
	m.handleStalePlan()

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

	di := textinput.New()
	di.Prompt = "/ "
	di.CharLimit = 100
	m.detailSearchInput = di

	pi := textinput.New()
	pi.Prompt = "> "
	pi.CharLimit = 50
	m.pickerInput = pi

	if !ProcessStart.IsZero() {
		m.startupDuration = time.Since(ProcessStart)
	}

	return m
}

func (m *Model) refreshProjects() {
	projects, _ := m.store.ListActiveProjects()
	cfg := loadTUIConfig(m.store.RootDir())
	m.projects = append([]string{"all"}, applyProjectOrder(projects, cfg.ProjectOrder)...)
	if m.activeProject >= len(m.projects) {
		m.activeProject = 0
	}
}

// applyProjectOrder sorts projects according to a saved order. Projects not in order go to the end.
func applyProjectOrder(projects []string, order []string) []string {
	if len(order) == 0 {
		return projects
	}
	pos := make(map[string]int, len(order))
	for i, slug := range order {
		pos[slug] = i
	}
	sorted := make([]string, len(projects))
	copy(sorted, projects)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi, oki := pos[sorted[i]]
		pj, okj := pos[sorted[j]]
		if oki && okj {
			return pi < pj
		}
		if oki {
			return true
		}
		return false
	})
	return sorted
}

// visibleProjects returns projects not hidden by the user. "all" is always visible.
func (m Model) visibleProjects() []string {
	var result []string
	for _, p := range m.projects {
		if p == "all" || !m.hiddenProjects[p] {
			result = append(result, p)
		}
	}
	return result
}

// nextVisibleProject finds the next non-hidden project index in direction dir (+1 or -1).
func (m Model) nextVisibleProject(dir int) int {
	n := len(m.projects)
	if n <= 1 {
		return 0
	}
	idx := m.activeProject
	for i := 0; i < n; i++ {
		idx = (idx + dir + n) % n
		p := m.projects[idx]
		if p == "all" || !m.hiddenProjects[p] {
			return idx
		}
	}
	return 0
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

	// refresh focus plan: remove done/archived/deleted tasks
	m.loadFocusPlan()
	allTasks, _ := m.store.GetAllTasks()
	if m.focusPlan.Cleanup(allTasks) {
		m.saveFocusPlan()
	}
}

func (m *Model) applyColumnVisibility() {
	var filtered []storage.TaskStatus
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
		if q != "" && !matchesQuery(t, q) {
			continue
		}
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Meta.Order != result[j].Meta.Order {
			return result[i].Meta.Order < result[j].Meta.Order
		}
		ni := taskIDNum(result[i].Meta.ID)
		nj := taskIDNum(result[j].Meta.ID)
		if ni != nj {
			return ni < nj
		}
		return result[i].Meta.Updated > result[j].Meta.Updated
	})
	return result
}

// matchesQuery checks if a task matches the search query across all fields.
// q must be pre-lowercased.
func matchesQuery(t *storage.Task, q string) bool {
	if strings.Contains(strings.ToLower(t.Meta.Title), q) {
		return true
	}
	if strings.Contains(strings.ToLower(t.Meta.ID), q) {
		return true
	}
	if strings.Contains(strings.ToLower(t.Body), q) {
		return true
	}
	if strings.Contains(strings.ToLower(t.Meta.Brief), q) {
		return true
	}
	if strings.Contains(strings.ToLower(t.Meta.AC), q) {
		return true
	}
	if strings.Contains(strings.ToLower(t.Meta.Branch), q) {
		return true
	}
	for _, tag := range t.Meta.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	for k, v := range t.Meta.Links {
		if strings.Contains(strings.ToLower(k), q) || strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	for _, sid := range t.Meta.Sessions {
		if strings.Contains(strings.ToLower(sid), q) {
			return true
		}
	}
	return false
}

// taskIDNum extracts the trailing number from a task ID (e.g. "proj-10" -> 10).
func taskIDNum(id string) int {
	if idx := strings.LastIndex(id, "-"); idx >= 0 {
		if n, err := strconv.Atoi(id[idx+1:]); err == nil {
			return n
		}
	}
	return 0
}

func (m Model) archivedTasks() []*storage.Task {
	var result []*storage.Task
	q := strings.ToLower(m.searchQuery)
	for _, t := range m.tasks {
		if t.Meta.Status != storage.StatusArchived {
			continue
		}
		if q != "" && !matchesQuery(t, q) {
			continue
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

// trackerBadges returns a "▸done+merged/total" progress badge keyed by tracker
// task ID (a parent that has children). Used to mark parent cards on the board.
func (m Model) trackerBadges() map[string]string {
	trackers, _ := storage.BuildTrackers(m.tasks)
	out := make(map[string]string, len(trackers))
	for _, tr := range trackers {
		complete := tr.Progress[string(storage.StatusDone)] + tr.Progress["merged"]
		out[tr.ID] = fmt.Sprintf("▸%d/%d", complete, tr.Total)
	}
	return out
}

// taskChildren returns the subtasks of t (tasks whose parent is t.ID), sorted by
// numeric ID. Empty if t is not a tracker.
func (m Model) taskChildren(t *storage.Task) []*storage.Task {
	if t == nil || t.Meta.ID == "" {
		return nil
	}
	var kids []*storage.Task
	for _, c := range m.tasks {
		if c.Meta.Parent == t.Meta.ID {
			kids = append(kids, c)
		}
	}
	sort.Slice(kids, func(i, j int) bool {
		ni, nj := taskIDNum(kids[i].Meta.ID), taskIDNum(kids[j].Meta.ID)
		if ni != nj {
			return ni < nj
		}
		return kids[i].Meta.ID < kids[j].Meta.ID
	})
	return kids
}

// taskParent returns the parent task of t, or nil if t has no parent (or the
// parent isn't in the loaded set).
func (m Model) taskParent(t *storage.Task) *storage.Task {
	if t == nil || t.Meta.Parent == "" {
		return nil
	}
	for _, p := range m.tasks {
		if p.Meta.ID == t.Meta.Parent {
			return p
		}
	}
	return nil
}

// markedTasks returns all tasks currently in the selection set.
func (m Model) markedTasks() []*storage.Task {
	var result []*storage.Task
	for _, t := range m.tasks {
		if m.selected[t.Meta.ID] {
			result = append(result, t)
		}
	}
	return result
}

// --- focus plan helpers ---

func (m *Model) rebuildFocusSet() {
	m.focusSet = make(map[string]bool, len(m.focusPlan.Tasks))
	for _, id := range m.focusPlan.Tasks {
		m.focusSet[id] = true
	}
}

func (m *Model) loadFocusPlan() {
	m.focusPlan = storage.ReadFocusPlan(m.store.RootDir())
	m.rebuildFocusSet()
}

func (m *Model) saveFocusPlan() {
	storage.WriteFocusPlan(m.store.RootDir(), m.focusPlan)
}

func (m *Model) handleStalePlan() {
	if !m.focusPlan.IsStale() {
		return
	}
	allTasks, _ := m.store.GetAllTasks()
	m.focusPlan.Cleanup(allTasks)
	n := len(m.focusPlan.Tasks)
	m.focusPlan.Date = storage.Today()
	m.saveFocusPlan()
	m.rebuildFocusSet()
	if n > 0 {
		m.toastMsg = fmt.Sprintf("Focus carried over from yesterday (%d tasks)", n)
		m.toastExpiry = time.Now().Add(4 * time.Second)
	}
}

func (m *Model) focusTasks() []*storage.Task {
	allTasks, _ := m.store.GetAllTasks()
	lookup := make(map[string]*storage.Task, len(allTasks))
	for _, t := range allTasks {
		lookup[t.Meta.ID] = t
	}
	var result []*storage.Task
	for _, id := range m.focusPlan.Tasks {
		if t, ok := lookup[id]; ok {
			result = append(result, t)
		}
	}
	return result
}

func (m Model) selectedFocusTask() *storage.Task {
	tasks := m.focusTasks()
	if len(tasks) == 0 {
		return nil
	}
	idx := m.focusCursor
	if idx >= len(tasks) {
		idx = len(tasks) - 1
	}
	return tasks[idx]
}

func (m *Model) fixFocusCursor() {
	tasks := m.focusTasks()
	if m.focusCursor >= len(tasks) && len(tasks) > 0 {
		m.focusCursor = len(tasks) - 1
	}
	if len(tasks) == 0 {
		m.focusCursor = 0
	}
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

type launchResultMsg struct {
	agent launchAgent
	kind  string
	err   error
}
