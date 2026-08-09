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
	viewRuns
	viewReport
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

// detailState is the task-detail / project-info view and its in-page search.
// Both views render into the same viewport (detailTask for a task, infoProject
// + infoSlug for a project), which is why they share one cluster.
type detailState struct {
	detailViewport viewport.Model
	detailTask     *storage.Task
	infoProject    *storage.Project
	infoSlug       string

	// detail search ("/" inside the detail view)
	detailSearching     bool
	detailSearchInput   textinput.Model
	detailSearchQuery   string
	detailSearchMatches []int // line numbers of matches
	detailSearchIdx     int
	detailPlainContent  string // ANSI-stripped for searching
}

// execView is the executor agent-view (18-7): the live transcript of the
// running worker, plus which run/session it is showing.
type execView struct {
	executorViewport  viewport.Model
	executorRunTaskID string            // task/tracker id whose run we're watching
	executorRunProj   string            // project slug of that run
	executorRun       *storage.RunState // latest run-state for the header
	// executorWatchFinish: which of the task's two run-states is on screen -
	// the acceptance when set, the run otherwise. The two share a task id, so
	// this (not the id) is what decides which file the view re-reads on every
	// tick; W toggles it. A bool rather than a kind string on purpose: there is
	// no kind to put in such a field for "the run" that is true of both `pm
	// work` and `pm run-epic`, and a field holding a kind the run does not have
	// is a trap for the next comparison somebody writes.
	executorWatchFinish bool
	// executorHasOther: the counterpart run-state exists on disk, so the W
	// toggle has somewhere to go. Re-checked on refresh, since an acceptance
	// routinely starts while its run is still being watched.
	executorHasOther   bool
	executorSessions   []execSession // sub entries that have a worker session, in order
	executorSessionIdx int           // which session is shown
	executorFollow     bool          // auto-scroll to the latest activity
	executorVerbose    bool          // full conversation (untruncated) vs compact feed
	executorPrevView   view          // where to return on esc (kept separate from previousView)
	// transcriptCaches: one incremental decode cache per transcript path, so
	// re-rendering on tick only re-reads/re-parses appended bytes (never a full
	// re-decode of an unchanged or already-seen prefix). Keyed by path rather
	// than session so switching worker tabs and back stays cheap too.
	transcriptCaches map[string]*transcriptCache
}

// runsView is the Runs list: one row per tracker, every project, this machine
// and - on demand - the remote runners. One floor ABOVE execView, which shows
// the transcript of ONE run and can only be reached through the task owning it.
type runsView struct {
	// runsRows is what is on screen: the local rows, re-read on every tick while
	// this view is open, merged with runsRemoteRows and ordered by
	// storage.SortRunRows so a remote row lands exactly where `pm runs` puts it.
	runsRows []storage.RunRow
	// runsRemoteRows is the last remote FETCH's answer, kept across ticks.
	// Remote rows are deliberately NOT part of the tick: each read is an ssh
	// round trip per machine (seconds, and a sleeping VPS costs the whole connect
	// timeout), which would stall the render loop every two seconds. They stay as
	// fetched - with their age in the header - until `f` fetches again.
	runsRemoteRows []storage.RunRow
	runsCursor     int
	runsScroll     int // first visible row
	// runsPrevView is where esc returns, kept separate from previousView for the
	// same reason execView has executorPrevView: Runs -> agent-view -> esc must
	// come back HERE, and the detail view's own return path has to survive a trip
	// through both.
	runsPrevView view
	runsFetching bool      // a remote fetch is in flight
	runsFetchAt  time.Time // when runsRemoteRows was last replaced
	runsFetchErr string    // why the last fetch failed, "" if it did not
}

// reportView is the acceptance report: the markdown an acceptance run leaves
// behind (storage.FinishReportPath), rendered in the board.
//
// Its own cluster rather than a second tenant of detailState's viewport,
// because the two are open at once: F is pressed FROM the detail view and esc
// returns to it with its scroll position and search state intact.
type reportView struct {
	reportViewport viewport.Model
	reportPath     string
	reportTaskID   string
	// reportPrevView is where esc returns - the same rule as executorPrevView
	// and runsPrevView, and for the same reason: F is reachable from the detail
	// view AND from the Runs list, and each has to get its own reader back.
	reportPrevView view
}

// launchMenu is the launch overlay (Claude / Codex / executor) and the
// per-launch choices made in it. resumeSessionID/resumeOnly/forkMode live here
// rather than with the session menu: the session menu only seeds them, the
// launch overlay and launchLLM are what consume them.
//
// Grouping is by concern, NOT by lifetime: the three openers in launch_menu.go
// deliberately reset different subsets (openClaudeMenu keeps the resume/fork
// choice the session menu just made, openExecutorMenu clears it), so do not
// "simplify" them into a single `m.launchMenu = launchMenu{...}`.
type launchMenu struct {
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
	// claudeMenuThenFinish = user asked THIS epic launch to chain a detached
	// acceptance (`--then-finish`) when the run ends. Tracker-only (the flag
	// exists only on `pm run-epic`); off = the flag is simply not passed, so
	// the tracker's own finish_mode keeps deciding (tri-state, run_epic.go).
	claudeMenuThenFinish bool
	// claudeMenuSim = user asked THIS acceptance to be allowed the simulator
	// (`pm finish --sim`). executorSimAvail = the project declares a runtime
	// skill at all (executor.handoff.runtime_skill), which is the only evidence
	// pm has that there IS a runtime to drive - without one the toggle is not
	// shown, since pm-cli and a linux runner have no simulator to offer.
	//
	// The toggle is a REQUEST: only the tmux launch honours it (resolveFinishSim),
	// because a detached acceptance touching a slot's runtime would break the
	// separation the executor and session locks rest on.
	claudeMenuSim    bool
	executorSimAvail bool
	// executorSlots = the project's worktree slot pool with live lock holders,
	// gathered when the executor launch menu opens (fresh at decision time) and
	// rendered under the # toggle so the user sees which slot a launch would get.
	executorSlots []executorSlotStatus

	// project-scope claude launch (no task)
	projectScopeLaunch bool
	projectScopeSlug   string

	// resume/fork choices carried over from the session menu
	resumeSessionID string // override for launchClaude resume
	resumeOnly      bool   // when true, claude menu shows only resume options
	forkMode        bool   // when true with resumeOnly, claude menu shows fork options
}

// pickerState is the two list-pick overlays: the project picker (board scope)
// and the subtask picker (parent -> child navigation in the detail view).
type pickerState struct {
	projectPicker bool
	pickerItems   []pickerItem
	pickerCursor  int
	pickerInput   textinput.Model
	pickerFilter  string

	subtaskPicker bool
	subtaskItems  []*storage.Task
	subtaskCursor int
}

// menuState groups the four small list-pick-and-act overlays left loose after
// 60-5 (yank/links/colVis/session): each is an open flag + item list + cursor,
// opened from several base views (board/detail/archive/focus/project-info) and
// closed on its own with no shared workflow beyond that shape - unlike
// launchMenu/pickerState, which group by concern, this one groups by identical
// structure. hiddenStatuses rides along with colVis: it is the persisted
// result of toggling colVisItems, not independent state.
type menuState struct {
	// yank menu
	yankMenu   bool
	yankItems  []yankItem
	yankCursor int

	// links menu
	linksMenu   bool
	linkItems   []linkItem
	linksCursor int

	// column visibility menu
	colVisMenu     bool
	colVisItems    []colVisItem
	colVisCursor   int
	hiddenStatuses map[storage.TaskStatus]bool

	// session menu
	sessionMenu      bool
	sessionMenuItems []sessionMenuItem
	sessionCursor    int
}

type Model struct {
	// Sub-struct clusters, embedded so every field stays reachable as
	// m.<field> (no call-site churn, no accessor layer).
	detailState
	execView
	runsView
	reportView
	launchMenu
	pickerState
	menuState

	store    storage.TaskStore
	tasks    []*storage.Task
	projects []string
	statuses []storage.TaskStatus
	// landing = where the executor parks a verify-green sub in the active tab's
	// project(s): done_status + done_status_independent, "merged"/"pushed" by
	// default. Cached by loadStatuses because the render path reads it per frame.
	landing       []storage.TaskStatus
	activeProject int // 0 = all, 1+ = specific project
	activeCol     int
	cursors       []int
	width         int
	height        int
	currentView   view
	previousView  view
	searchInput   textinput.Model
	searchQuery   string
	searching     bool
	showHelp      bool
	adding        bool
	addStep       int // 0=title, 1=ID
	addInput      textinput.Model
	addTitle      string

	// confirmation
	confirmAction string // "" | "done" | "delete"
	confirmTaskID string
	// confirmRunFinish: for "kill-run", WHICH of the task's two runs the
	// pending confirmation is for. The task id does not say - a run and its
	// acceptance share it - and the two are routinely live together, so a run
	// that ends between the two K presses would otherwise turn a confirmation
	// given for the run into a kill of the acceptance.
	confirmRunFinish bool

	// archive
	archiveCursor int

	// project counts for tabs
	projectCounts map[string]int

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

	// zoom
	zoomed bool

	// multi-select
	selecting bool
	selected  map[string]bool // task Meta.ID -> true

	// executor run-states (live background runs), keyed by task/tracker id;
	// refreshed on tick from <project>/.executor/*.json
	runStates map[string]*storage.RunState

	// finishStates: acceptance (`pm finish`) run-states, keyed by the id of the
	// tracker each one accepts - refreshed from the same dir on the same tick.
	//
	// A SECOND map, never merged into runStates: the two share a key (the
	// tracker's id), so one map would have them evict each other whichever way
	// the directory happened to sort - exactly the collision pm-cli-100-2 split
	// apart on disk. And they are genuinely concurrent, not alternatives:
	// `batch-finish-auto` is built to start before the run it accepts has
	// finished, accepting each sub as it lands.
	finishStates map[string]*storage.RunState

	startupDuration time.Duration

	// focus plan
	focusCursor     int
	focusPlan       storage.FocusPlan
	focusSet        map[string]bool          // O(1) lookup for card rendering
	focusTaskLookup map[string]*storage.Task // ID->task cache, rebuilt in reload() so focusTasks() never re-reads the store

	// hidden projects (not shown in tab bar)
	hiddenProjects map[string]bool
}

type tickMsg time.Time

const refreshInterval = 2 * time.Second

type reloadMsg struct{}

type launchResultMsg struct {
	agent launchAgent
	kind  string
	err   error
}

// editorResultMsg is openEditor's tea.ExecProcess callback result. A separate
// type from launchResultMsg (rather than reusing it with a synthetic agent)
// because openEditor never carries a launchAgent - the editor isn't one of
// claude/codex/executor.
type editorResultMsg struct {
	err error
}
