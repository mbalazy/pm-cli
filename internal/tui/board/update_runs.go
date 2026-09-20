package board

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/mbalazy/pm-cli/internal/tui/common"
)

// The Runs view's keyboard, plus the one thing this screen does that the rest of
// the board does not: reach other machines.

// openRunsView enters the Runs view from whatever view asked for it.
func (m *Model) openRunsView() {
	m.runsPrevView = m.currentView
	m.currentView = viewRuns
	m.runsCursor = 0
	m.runsScroll = 0
	// A confirmation armed on the board must not survive into a view whose keys
	// mean something else (the invariant overlayLadder's comment spells out).
	m.confirmAction = ""
	m.confirmTaskID = ""
	m.refreshRunsView()
}

func (m Model) updateRuns(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, common.Keys.Escape), key.Matches(msg, common.Keys.Quit),
		key.Matches(msg, common.Keys.RunsView):
		// Back to the view this was opened FROM, not to the board: R is reachable
		// from the board today, and the return path should not have to be revisited
		// when it is reachable from somewhere else.
		m.currentView = m.runsPrevView
		return m, nil

	case key.Matches(msg, common.Keys.Up):
		if m.runsCursor > 0 {
			m.runsCursor--
			m.fixRunsCursor()
		}

	case key.Matches(msg, common.Keys.Down):
		if m.runsCursor < len(m.runsRows)-1 {
			m.runsCursor++
			m.fixRunsCursor()
		}

	case key.Matches(msg, common.Keys.JumpTop):
		m.runsCursor = 0
		m.fixRunsCursor()

	case key.Matches(msg, common.Keys.JumpBottom):
		if len(m.runsRows) > 0 {
			m.runsCursor = len(m.runsRows) - 1
			m.fixRunsCursor()
		}

	case key.Matches(msg, common.Keys.HalfDown):
		m.runsCursor = min(m.runsCursor+m.runsHalfPage(), max(0, len(m.runsRows)-1))
		m.fixRunsCursor()

	case key.Matches(msg, common.Keys.HalfUp):
		m.runsCursor = max(m.runsCursor-m.runsHalfPage(), 0)
		m.fixRunsCursor()

	case key.Matches(msg, common.Keys.Enter), key.Matches(msg, common.Keys.Open),
		key.Matches(msg, common.Keys.Space):
		m.openRunsRow()
		return m, nil

	case key.Matches(msg, common.Keys.FinishReport):
		m.openRowReport()
		return m, nil

	case msg.String() == "t":
		m.openRunsRowDetail()
		return m, nil

	case msg.String() == "f":
		// The call is a STATEMENT, not an operand of the return: it takes a
		// pointer to this copy of m and sets runsFetching, and Go orders only the
		// calls in an expression - where the bare `m` operand is evaluated
		// relative to them is unspecified. Sequenced this way the "fetching…"
		// state cannot be dropped by a legal reordering.
		cmd := m.startRemoteRunsFetch()
		return m, cmd

	case msg.String() == "r":
		m.refreshRunsView()
		m.toastMsg = "Refreshed (local)"
		m.toastExpiry = time.Now().Add(2 * time.Second)
	}
	return m, nil
}

// runsHalfPage is how far Ctrl+d/Ctrl+u jump: half of what is on screen, so the
// step means the same thing in a 24-line terminal and a 60-line one.
func (m Model) runsHalfPage() int {
	if n := m.runsRowBudget() / 2; n > 1 {
		return n
	}
	return 1
}

// openRunsRow opens the agent-view for the run under the cursor.
func (m *Model) openRunsRow() {
	t := m.resolveRunsRowTask("transcript")
	if t == nil {
		return
	}
	// openExecutorView stamps executorPrevView from the CURRENT view, which is
	// still viewRuns here - that is what makes esc out of the agent-view come
	// back to this list instead of the board.
	if !m.openExecutorView(t) {
		m.toastMsg = "no run recorded for " + t.Meta.ID + " yet"
		m.toastExpiry = time.Now().Add(3 * time.Second)
	}
}

// openRunsRowDetail opens the task-detail view on the tracker under the cursor
// - the row says how the run went, the task says what the work was.
func (m *Model) openRunsRowDetail() {
	t := m.resolveRunsRowTask("task")
	if t == nil {
		return
	}
	// previousView is detail's own return pointer, so esc (and the status keys
	// that leave detail) come back to this list, the same way the agent-view's
	// executorPrevView does.
	m.previousView = viewRuns
	m.currentView = viewDetail
	m.openDetailTask(t)
}

// resolveRunsRowTask turns the row under the cursor into its LOCAL task,
// switching the board's project tab first when the row belongs to another one -
// the board's run-state and task maps only cover the tab that is showing, so
// without the switch the caller's view would open on nothing. Returns nil after
// saying why in a toast; `what` names the thing the caller wanted (transcript,
// task), because a remote row's refusal should say what it is that lives on the
// other machine.
func (m *Model) resolveRunsRowTask(what string) *storage.Task {
	row := m.selectedRunRow()
	if row == nil {
		return nil
	}
	if row.Remote != "" {
		// The task (and the run's transcript) live on the OTHER machine. pm could
		// reach over ssh, but that is a feature (and a failure mode) of its own;
		// this row's job is to say the run exists and where.
		m.toastMsg = "that run is on " + row.Remote + " - its " + what + " lives there, not here"
		m.toastExpiry = time.Now().Add(4 * time.Second)
		return nil
	}
	if row.Tracker == "" {
		m.toastMsg = "this row has no tracker to open"
		m.toastExpiry = time.Now().Add(3 * time.Second)
		return nil
	}
	// The tab list is re-read FIRST. m.projects is filled at startup and by the
	// board's own `r`, never by reload() - while the rows under this cursor come
	// off disk on every tick. So a project created since the board started is on
	// screen here and absent from the slice, and resolving against the stale copy
	// would answer "archived?" about a project that is neither archived nor gone.
	//
	// What the ACTIVE TAB names is remembered across that refresh. refreshProjects
	// rewrites the slice and can clamp activeProject, but only reload() rebuilds
	// m.tasks/statuses/landing/cursors/counts - and the board has no periodic
	// reload to repair them afterwards (the tick deliberately refreshes run-states
	// only). A tab that comes out of the refresh naming a DIFFERENT project than
	// it named going in must therefore be reloaded, even when the row needs no
	// switch at all: a project inserted before it shifts every later index, and an
	// archived one can clamp it to ALL.
	prevActive := ""
	if m.activeProject >= 0 && m.activeProject < len(m.projects) {
		prevActive = m.projects[m.activeProject]
	}
	m.refreshProjects()
	staleTab := m.activeProject >= len(m.projects) || m.projects[m.activeProject] != prevActive

	// On the ALL tab every project is already loaded, so there is nothing to
	// switch and no reason to pay for a reload.
	target := m.activeProject
	if m.activeProject != 0 && m.projects[m.activeProject] != row.Project {
		idx := -1
		for i, p := range m.projects {
			if p == row.Project {
				idx = i
				break
			}
		}
		switch {
		case idx < 0:
			// The project has no tab even after the refresh: archived, or removed
			// under the board. Its run-states are still on disk, but there is
			// nothing to switch to - say which project rather than open an empty view.
			if staleTab {
				m.reload()
			}
			m.toastMsg = "project " + row.Project + " is not on this board (archived?)"
			m.toastExpiry = time.Now().Add(4 * time.Second)
			return nil
		case m.hiddenProjects[row.Project]:
			// A hidden project is IN m.projects but has no tab (renderTabs marks
			// the active one among the VISIBLE projects only), so selecting it by
			// index would leave the board showing its tasks with no tab lit and no
			// way to tell which project that is. ALL is always visible and its
			// run-state refresh covers every project - which is all this path
			// needs - so descend through it rather than silently un-hiding a
			// project the user chose to hide.
			target = 0
		default:
			target = idx
		}
	}
	if target != m.activeProject || staleTab {
		m.activeProject = target
		m.reload()
	}
	// The maps are what pickRunForTask (and detail's run dashboard) read, and
	// the tab may have just changed.
	m.refreshRunStates()
	t, err := m.store.FindTask(row.Project, row.Tracker)
	if err != nil {
		m.showErrorToast("open run", err)
		return nil
	}
	return t
}

// openRowReport opens the acceptance report of the row under the cursor.
//
// Unlike openRunsRow this switches no tab and reloads nothing: the report is a
// file read by path, so which project the board happens to be showing does not
// come into it.
func (m *Model) openRowReport() {
	row := m.selectedRunRow()
	if row == nil {
		return
	}
	if row.Remote != "" {
		// Same boundary as the transcript: the file is on the other machine.
		m.toastMsg = "that acceptance ran on " + row.Remote + " - its report lives there, not here"
		m.toastExpiry = time.Now().Add(4 * time.Second)
		return
	}
	if row.Tracker == "" {
		m.toastMsg = "this row has no tracker to open"
		m.toastExpiry = time.Now().Add(3 * time.Second)
		return
	}
	m.openFinishReport(row.Project, row.Tracker)
}

// runsRemoteMsg carries one remote fetch's answer back into Update.
type runsRemoteMsg struct {
	rows []storage.RunRow
	err  error
}

// runsRemoteFetch is the seam the fetch goes through, so a test can drive the
// whole f -> rows-on-screen flow without an ssh anywhere near it.
var runsRemoteFetch = execRemoteRunRows

// runsRemoteTimeout bounds the WHOLE fetch, every configured machine included.
// The child caps each remote at its own 20s, so this is the backstop for a
// registry that has grown, not the per-machine deadline.
var runsRemoteTimeout = 2 * time.Minute

// runsFetchWaitDelay bounds how long Wait may block AFTER the deadline has
// killed the child, on behalf of descendants still holding its output pipe.
const runsFetchWaitDelay = 5 * time.Second

// startRemoteRunsFetch kicks off the remote fetch and returns the tea.Cmd that
// delivers it. Asynchronous by construction: a fetch is seconds of ssh, and the
// board must keep rendering (and stay quittable) throughout.
func (m *Model) startRemoteRunsFetch() tea.Cmd {
	if m.runsFetching {
		m.toastMsg = "already fetching"
		m.toastExpiry = time.Now().Add(2 * time.Second)
		return nil
	}
	// The registry is read HERE so the common case - no remote runners at all -
	// costs no subprocess and gets a sentence saying why nothing happened. An
	// unparsable config is an error, matching storage.LoadConfig's rule: a YAML
	// typo must never read as "you have no remote runners".
	cfg, err := m.store.LoadConfig()
	if err != nil {
		m.showErrorToast("global config", err)
		return nil
	}
	if len(cfg.Remotes) == 0 {
		m.toastMsg = "no remote runners declared (see `pm config show`)"
		m.toastExpiry = time.Now().Add(4 * time.Second)
		return nil
	}
	m.runsFetching = true
	m.runsFetchErr = ""
	return func() tea.Msg {
		rows, err := runsRemoteFetch()
		return runsRemoteMsg{rows: rows, err: err}
	}
}

// applyRemoteRuns folds a finished fetch into the view.
//
// A FAILED fetch keeps the rows already fetched rather than blanking them: rows
// from ten minutes ago plus a visible error beat an empty ACCEPTANCE column that
// reads as "nothing is running there".
func (m *Model) applyRemoteRuns(msg runsRemoteMsg) {
	m.runsFetching = false
	if msg.err != nil {
		m.runsFetchErr = msg.err.Error()
		m.showErrorToast("remote runs", msg.err)
	} else {
		m.runsRemoteRows = msg.rows
		m.runsFetchAt = time.Now()
		m.toastMsg = fmt.Sprintf("fetched %d remote row(s)", len(msg.rows))
		m.toastExpiry = time.Now().Add(3 * time.Second)
	}
	m.refreshRunsView()
}

// execRemoteRunRows asks THIS pm's CLI for the remote rows: `pm runs --json`,
// keeping only the rows that came from another machine.
//
// A SUBPROCESS, not a function call, because the ssh half of the aggregation
// lives in internal/cmd - which imports this package, so importing it back is a
// cycle. `pm runs --json` is the contract that half was built to expose (its
// payload is an object with a rows key precisely so the board could parse it),
// and the local rows this view shows still come from storage.LocalRunRows
// in-process, so there is exactly one counting rule either way. The child's own
// local rows are DISCARDED here: they would be a second, staler copy of rows
// this process already has.
//
// The binary is os.Executable(): the board and its child must be the same pm,
// or a stale binary earlier in PATH would answer for a feature this build has
// and it does not. PATH is consulted only as a last resort, when this process
// cannot name its own executable at all.
func execRemoteRunRows() ([]storage.RunRow, error) {
	exe, err := os.Executable()
	if err != nil {
		exe, err = exec.LookPath("pm")
		if err != nil {
			return nil, fmt.Errorf("cannot locate the pm binary: %w", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), runsRemoteTimeout)
	defer cancel()

	// Its OWN process group, with a Cancel that signals the group and a
	// WaitDelay behind it - a BOUND ON WAIT, not a fix for a reachable hang.
	// What makes the difference matter: this runs on a tea.Cmd goroutine, so a
	// Wait that never returns means runsRemoteMsg never arrives and runsFetching
	// stays true for the rest of the session, every later `f` answering "already
	// fetching". Today nothing can produce that - the child buffers each ssh into
	// a pipe of its own (internal/cmd's fetchRemoteRuns gives it bytes.Buffers),
	// so no grandchild ever holds the write end of OUR pipe and the deadline's
	// kill of the direct child is enough to unblock Wait. The bound is here so
	// that stops being something this file has to keep being true about a command
	// in another package. Note its reach: internal/cmd's groupCmd puts each ssh in
	// a group of its own, so this signal covers the child and anything that stayed
	// with it, never those. groupCmd itself cannot be reused - internal/cmd
	// imports this package.
	c := exec.CommandContext(ctx, exe, "runs", "--json")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if p := c.Process; p != nil && p.Pid > 0 {
			// Negative pid = the whole group. Never 0 or less: that is OUR group.
			_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
		}
		return nil // let Wait report the process's own error, not this one
	}
	c.WaitDelay = runsFetchWaitDelay
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr

	// exec.ErrWaitDelay is NOT a failure - it means the child exited fine but
	// something it left behind still held the output pipe, so Wait unblocked us.
	// The answer is already in the buffer; discarding it would report a fetch
	// that in fact succeeded. Same rule as internal/cmd's fetchRemoteRuns.
	runErr := c.Run()
	if ctx.Err() != nil || errors.Is(runErr, exec.ErrWaitDelay) {
		// Wait came back on a deadline or a WaitDelay, which is exactly the case
		// where the group can still have members. runGroupCmd's own follow-up, and
		// the half a bound without it leaves out: unblocking ourselves is not the
		// same as leaving nothing behind.
		if p := c.Process; p != nil && p.Pid > 0 {
			_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
		}
	}
	if runErr != nil && !errors.Is(runErr, exec.ErrWaitDelay) {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("no answer within %s", runsRemoteTimeout)
		}
		if s := firstLine(stderr.String()); s != "" {
			return nil, fmt.Errorf("%v: %s", runErr, s)
		}
		return nil, runErr
	}
	var payload struct {
		Rows []storage.RunRow `json:"rows"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return nil, fmt.Errorf("`pm runs --json` answered with something unparsable: %w", err)
	}
	var remote []storage.RunRow
	for _, r := range payload.Rows {
		if r.Remote != "" {
			remote = append(remote, r)
		}
	}
	return remote, nil
}
