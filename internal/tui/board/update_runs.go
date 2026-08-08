package board

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/tui/common"
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

	case msg.String() == "f":
		return m, m.startRemoteRunsFetch()

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

// openRunsRow opens the agent-view for the run under the cursor, switching the
// board's project tab first when the row belongs to another one - the board's
// run-state maps only cover the tab that is showing, so without the switch the
// view would open on nothing.
func (m *Model) openRunsRow() {
	row := m.selectedRunRow()
	if row == nil {
		return
	}
	if row.Remote != "" {
		// The transcript is a file on the OTHER machine. pm could stream it over
		// ssh, but that is a feature (and a failure mode) of its own; this row's
		// job is to say the run exists and where.
		m.toastMsg = "that run is on " + row.Remote + " - its transcript lives there, not here"
		m.toastExpiry = time.Now().Add(4 * time.Second)
		return
	}
	if row.Tracker == "" {
		m.toastMsg = "this row has no tracker to open"
		m.toastExpiry = time.Now().Add(3 * time.Second)
		return
	}
	// On the ALL tab every project is already loaded, so there is nothing to
	// switch and no reason to pay for a reload.
	if m.activeProject != 0 && m.projects[m.activeProject] != row.Project {
		idx := -1
		for i, p := range m.projects {
			if p == row.Project {
				idx = i
				break
			}
		}
		if idx < 0 {
			// An archived project still has run-states and tasks on disk, but no
			// tab to switch to - say which project rather than open an empty view.
			m.toastMsg = "project " + row.Project + " is not on this board (archived?)"
			m.toastExpiry = time.Now().Add(4 * time.Second)
			return
		}
		m.activeProject = idx
		m.reload()
	}
	// The maps are what pickRunForTask reads, and the tab may have just changed.
	m.refreshRunStates()
	t, err := m.store.FindTask(row.Project, row.Tracker)
	if err != nil {
		m.showErrorToast("open run", err)
		return
	}
	// openExecutorView stamps executorPrevView from the CURRENT view, which is
	// still viewRuns here - that is what makes esc out of the agent-view come
	// back to this list instead of the board.
	if !m.openExecutorView(t) {
		m.toastMsg = "no run recorded for " + row.Tracker + " yet"
		m.toastExpiry = time.Now().Add(3 * time.Second)
	}
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
// The binary is os.Executable(), never a bare "pm" from PATH: the board and its
// child must be the same pm, or a stale binary earlier in PATH would answer for
// a feature this build has and it does not.
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

	out, err := exec.CommandContext(ctx, exe, "runs", "--json").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("no answer within %s", runsRemoteTimeout)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%v: %s", err, firstLine(string(ee.Stderr)))
		}
		return nil, err
	}
	var payload struct {
		Rows []storage.RunRow `json:"rows"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
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
