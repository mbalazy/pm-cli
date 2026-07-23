package board

import (
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
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
	// executor "additional" worktree choice, made per launch (like skip-perms).
	// claudeMenuAdditional = user picked an isolated worktree slot for THIS
	// launch (first free slot claimed at run time); executorAdditionalAvail =
	// the project has at least one slot configured (executor.worktrees, or the
	// legacy additional_worktree pair).
	claudeMenuAdditional    bool
	executorAdditionalAvail bool

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

type tickMsg time.Time

const refreshInterval = 2 * time.Second

type reloadMsg struct{}

type launchResultMsg struct {
	agent launchAgent
	kind  string
	err   error
}
