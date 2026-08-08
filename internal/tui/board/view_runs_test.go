package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
)

// The Runs view: the screen pm did not have - every tracker's run and its
// acceptance, across projects and machines, one floor above the single-run
// agent-view.

// runsTracker builds a tracker (a task with children) so storage.LocalRunRows
// produces a row for it - a row IS a tracker, standalone `pm work` tasks get
// none.
func runsTracker(t *testing.T, m *Model, project, id string, children int) {
	t.Helper()
	if err := m.store.AddTask(project, &storage.Task{
		Meta: storage.TaskMeta{ID: id, Title: "Batch " + id, Status: storage.StatusDoing},
	}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= children; i++ {
		kid := fmt.Sprintf("%s-%d", id, i)
		if err := m.store.AddTask(project, &storage.Task{
			Meta: storage.TaskMeta{ID: kid, Title: "sub " + kid, Status: storage.StatusDone, Parent: id},
		}); err != nil {
			t.Fatal(err)
		}
	}
	m.reload()
}

// newRunsModel is a board sitting in the Runs view with one tracker that has a
// finished run and a finished acceptance.
func newRunsModel(t *testing.T) *Model {
	t.Helper()
	m := newBoardModel(t)
	runsTracker(t, m, "p", "p-9", 2)
	stateDir := m.store.ProjectDir("p")
	run := &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: storage.RunKindEpic, Status: storage.RunStatusDone,
		Started: time.Now().UTC().Format(time.RFC3339), Updated: time.Now().UTC().Format(time.RFC3339),
		Subs: []storage.SubRun{
			{ID: "p-9-1", Status: "merged"},
			{ID: "p-9-2", Status: "merged"},
		},
	}
	if err := storage.WriteRunState(stateDir, run); err != nil {
		t.Fatal(err)
	}
	accept := &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: storage.RunKindFinish, Status: storage.RunStatusDone,
		Started: time.Now().UTC().Format(time.RFC3339), Updated: time.Now().UTC().Format(time.RFC3339),
		Subs: []storage.SubRun{{ID: "p-9", Status: "partial", VisualClaimsOpen: 3}},
	}
	if err := storage.WriteRunState(stateDir, accept); err != nil {
		t.Fatal(err)
	}
	m.openRunsView()
	return m
}

// The row's numbers and words are storage's (RunCell/AcceptCell), never
// recounted here - that is the whole reason the aggregation lives one layer
// down, and a board that counted for itself is how the CLI and the TUI start
// disagreeing about the same tracker.
func TestViewRunsRendersTheAggregationVerbatim(t *testing.T) {
	m := newRunsModel(t)
	out := stripANSI(m.viewRuns())

	for _, want := range []string{"pm runs", "PROJECT", "TRACKER", "ACCEPTANCE", "p-9", "Batch p-9"} {
		if !strings.Contains(out, want) {
			t.Errorf("the view is missing %q:\n%s", want, out)
		}
	}
	// The run finished both its subs, and the tracker has exactly those two.
	if !strings.Contains(out, "done 2/2") {
		t.Errorf("RUN cell should read %q:\n%s", "done 2/2", out)
	}
	// The acceptance's OWN verdict, and the open visual claims that outlive it -
	// the number that is the user's morning TODO list.
	if !strings.Contains(out, "partial") || !strings.Contains(out, "3 visual claim(s) open") {
		t.Errorf("ACCEPTANCE cell should carry the verdict and the open claims:\n%s", out)
	}
	// The remote half is opt-in, and the view says so rather than leaving an
	// empty column that reads as "nothing runs there".
	if !strings.Contains(out, "not fetched") {
		t.Errorf("the state line should say the remote rows were never fetched:\n%s", out)
	}
}

// viewRuns must render EXACTLY m.height visual lines: bubbletea cuts the TOP off
// anything taller, so an overshoot costs the title rather than the last row.
func TestViewRunsRendersExactlyHeightLines(t *testing.T) {
	full := newRunsModel(t)
	// More rows than any of these terminals can show, so the scroll indicators
	// are part of what is being measured.
	for i := 1; i <= 40; i++ {
		runsTracker(t, full, "p", fmt.Sprintf("p-%d", 100+i), 1)
	}
	full.refreshRunsView()
	if len(full.runsRows) < 40 {
		t.Fatalf("expected a long row list, got %d", len(full.runsRows))
	}

	empty := newBoardModel(t)
	empty.openRunsView()
	if len(empty.runsRows) != 0 {
		t.Fatalf("expected no rows, got %d", len(empty.runsRows))
	}

	for _, tc := range []struct {
		name string
		m    *Model
	}{{"many rows", full}, {"no rows", empty}} {
		for _, h := range []int{20, 24, 30, 40, 60} {
			for _, w := range []int{80, 120, 200} {
				t.Run(fmt.Sprintf("%s h=%d w=%d", tc.name, h, w), func(t *testing.T) {
					tc.m.height, tc.m.width = h, w
					tc.m.fixRunsCursor()
					if got := strings.Count(tc.m.viewRuns(), "\n") + 1; got != h {
						t.Errorf("viewRuns rendered %d lines, want height %d", got, h)
					}
				})
			}
		}
		// The cursor at the very bottom is the case that scrolls, and it must not
		// change the line count either.
		t.Run(tc.name+" cursor at the bottom", func(t *testing.T) {
			tc.m.height, tc.m.width = 24, 120
			tc.m.runsCursor = max(0, len(tc.m.runsRows)-1)
			tc.m.fixRunsCursor()
			if got := strings.Count(tc.m.viewRuns(), "\n") + 1; got != 24 {
				t.Errorf("viewRuns rendered %d lines, want 24", got)
			}
			if tc.m.runsCursor >= len(tc.m.runsRows) && len(tc.m.runsRows) > 0 {
				t.Errorf("cursor %d escaped %d rows", tc.m.runsCursor, len(tc.m.runsRows))
			}
		})
	}
}

// Zero runs is a normal state (a fresh install, or every project without a
// tracker): it renders, it says why, and it leaves the cursor somewhere legal.
func TestViewRunsEmptyState(t *testing.T) {
	m := newBoardModel(t)
	m.openRunsView()

	out := stripANSI(m.viewRuns())
	if !strings.Contains(out, "No trackers found.") {
		t.Errorf("empty state should say so:\n%s", out)
	}
	if m.runsCursor != 0 || m.runsScroll != 0 {
		t.Errorf("cursor/scroll = %d/%d, want 0/0 with no rows", m.runsCursor, m.runsScroll)
	}
	if m.selectedRunRow() != nil {
		t.Error("selectedRunRow must be nil with no rows")
	}
	// Navigation keys on an empty list are the classic cursor-escape bug.
	for _, k := range []tea.KeyMsg{keyRunes('j'), keyRunes('k'), keyRunes('g'), keyRunes('G'),
		{Type: tea.KeyCtrlD}, {Type: tea.KeyCtrlU}, {Type: tea.KeyEnter}} {
		res, _ := m.updateRuns(k)
		got := res.(Model)
		if got.runsCursor != 0 || got.runsScroll != 0 {
			t.Errorf("key %q moved the cursor to %d/%d on an empty list", k.String(), got.runsCursor, got.runsScroll)
		}
	}
}

// The navigation contract: R opens the view, esc returns to whichever view it
// was opened from, enter descends into the agent-view of that row's run, and esc
// from THERE comes back to the list - not to the board. That last hop is the
// reason viewExecutor keeps its own executorPrevView, and why this view keeps
// runsPrevView instead of sharing previousView.
func TestRunsViewNavigation(t *testing.T) {
	t.Run("R opens it from the board and esc goes back", func(t *testing.T) {
		m := newBoardModel(t)
		runsTracker(t, m, "p", "p-9", 1)
		res, _ := m.updateBoard(keyRunes('R'))
		opened := res.(Model)
		if opened.currentView != viewRuns {
			t.Fatalf("R should open the Runs view, currentView = %v", opened.currentView)
		}
		if opened.runsPrevView != viewBoard {
			t.Errorf("runsPrevView = %v, want viewBoard", opened.runsPrevView)
		}
		if len(opened.runsRows) == 0 {
			t.Error("opening the view should load the rows")
		}
		back, _ := opened.updateRuns(tea.KeyMsg{Type: tea.KeyEsc})
		if got := back.(Model).currentView; got != viewBoard {
			t.Errorf("esc returned to %v, want viewBoard", got)
		}
	})

	t.Run("esc returns to the view it was opened from, not always the board", func(t *testing.T) {
		m := newRunsModel(t)
		m.currentView = viewFocus
		m.openRunsView()
		back, _ := m.updateRuns(tea.KeyMsg{Type: tea.KeyEsc})
		if got := back.(Model).currentView; got != viewFocus {
			t.Errorf("esc returned to %v, want the view it came from (viewFocus)", got)
		}
	})

	t.Run("enter opens the agent-view and esc comes back to Runs", func(t *testing.T) {
		m := newRunsModel(t)
		// Land the cursor on the tracker that has a run.
		found := false
		for i, r := range m.runsRows {
			if r.Tracker == "p-9" {
				m.runsCursor = i
				found = true
			}
		}
		if !found {
			t.Fatalf("no row for p-9: %+v", m.runsRows)
		}
		res, _ := m.updateRuns(tea.KeyMsg{Type: tea.KeyEnter})
		exec := res.(Model)
		if exec.currentView != viewExecutor {
			t.Fatalf("enter should open the agent-view, currentView = %v (toast %q)", exec.currentView, exec.toastMsg)
		}
		if exec.executorRunTaskID != "p-9" {
			t.Errorf("agent-view opened on %q, want p-9", exec.executorRunTaskID)
		}
		if exec.executorPrevView != viewRuns {
			t.Errorf("executorPrevView = %v, want viewRuns", exec.executorPrevView)
		}
		back, _ := exec.updateExecutorView(tea.KeyMsg{Type: tea.KeyEsc})
		if got := back.(Model).currentView; got != viewRuns {
			t.Errorf("esc out of the agent-view returned to %v, want viewRuns", got)
		}
	})

	t.Run("enter on another project's row switches the tab first", func(t *testing.T) {
		m := newRunsModel(t)
		if err := m.store.CreateProject("q", &storage.Project{Name: "Q"}); err != nil {
			t.Fatal(err)
		}
		m.refreshProjects()
		runsTracker(t, m, "q", "q-4", 1)
		if err := storage.WriteRunState(m.store.ProjectDir("q"), &storage.RunState{
			TaskID: "q-4", Project: "q", Kind: storage.RunKindEpic, Status: storage.RunStatusDone,
			Started: time.Now().UTC().Format(time.RFC3339),
			Subs:    []storage.SubRun{{ID: "q-4-1", Status: "merged"}},
		}); err != nil {
			t.Fatal(err)
		}
		m.refreshRunsView()
		for i, r := range m.runsRows {
			if r.Tracker == "q-4" {
				m.runsCursor = i
			}
		}
		if r := m.selectedRunRow(); r == nil || r.Project != "q" {
			t.Fatalf("cursor is not on q's row: %+v", r)
		}
		m.openRunsRow()
		if m.currentView != viewExecutor {
			t.Fatalf("the agent-view did not open (toast %q)", m.toastMsg)
		}
		if got := m.projects[m.activeProject]; got != "q" {
			t.Errorf("active project = %q, want q - the run-state maps only cover the visible tab", got)
		}
	})

	t.Run("a remote row says where its transcript lives instead of opening nothing", func(t *testing.T) {
		m := newRunsModel(t)
		m.runsRemoteRows = []storage.RunRow{{
			Remote: "runner", Project: "orbit", Tracker: "orbit-vps-3", Title: "remote batch",
			Updated: time.Now().UTC().Format(time.RFC3339),
			Run:     storage.RunCell{State: storage.RunCellRunning, Done: 1, Total: 4},
		}}
		m.refreshRunsView()
		for i, r := range m.runsRows {
			if r.Remote == "runner" {
				m.runsCursor = i
			}
		}
		m.openRunsRow()
		if m.currentView == viewExecutor {
			t.Error("a remote run has no local transcript - the agent-view must not open on it")
		}
		if !strings.Contains(m.toastMsg, "runner") {
			t.Errorf("toast = %q, want it to name the machine", m.toastMsg)
		}
	})
}

// No key may panic in this view - the pattern TestBoardKeysWithAllColumnsHidden
// established for the board. Run over both a populated and an empty list,
// because most cursor bugs only bite on one of the two.
func TestRunsViewKeysNeverPanic(t *testing.T) {
	populated := newRunsModel(t)
	empty := newBoardModel(t)
	empty.openRunsView()

	keys := []tea.KeyMsg{
		{Type: tea.KeyEsc}, {Type: tea.KeyEnter}, {Type: tea.KeyTab}, {Type: tea.KeyShiftTab},
		{Type: tea.KeyCtrlD}, {Type: tea.KeyCtrlU}, {Type: tea.KeyCtrlJ}, {Type: tea.KeyCtrlK},
		{Type: tea.KeyUp}, {Type: tea.KeyDown}, {Type: tea.KeyLeft}, {Type: tea.KeyRight},
		{Type: tea.KeySpace},
	}
	for r := 'a'; r <= 'z'; r++ {
		keys = append(keys, keyRunes(r), keyRunes(r-32)) // both cases
	}
	for _, name := range []string{"populated", "empty"} {
		m := populated
		if name == "empty" {
			m = empty
		}
		for _, k := range keys {
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Fatalf("%s: key %q panicked: %v", name, k.String(), rec)
					}
				}()
				res, _ := m.updateRuns(k)
				got := res.(Model)
				// Whatever a key did, the view must still render.
				_ = got.View()
			}()
		}
	}
}

// The remote half: fetched only on demand, merged into the same sorted list as
// the local rows, and a failure never throws away what was already fetched.
func TestRunsRemoteFetch(t *testing.T) {
	withRemoteConfig := func(t *testing.T, m *Model) {
		t.Helper()
		cfg := "remotes:\n  - name: runner\n    ssh: runner\n    pm: /home/runner/go/bin/pm\n    root: /home/runner/.claude/pm\n"
		if err := os.WriteFile(filepath.Join(m.store.RootDir(), storage.ConfigFileName), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	remoteRow := storage.RunRow{
		Remote: "runner", Project: "orbit", Tracker: "orbit-vps-7", Title: "night batch",
		Updated: time.Now().UTC().Format(time.RFC3339),
		Run:     storage.RunCell{State: storage.RunCellRunning, Done: 2, Total: 6},
	}
	swapFetch := func(t *testing.T, fn func() ([]storage.RunRow, error)) {
		t.Helper()
		prev := runsRemoteFetch
		runsRemoteFetch = fn
		t.Cleanup(func() { runsRemoteFetch = prev })
	}

	t.Run("no declared remotes costs no subprocess and says why", func(t *testing.T) {
		m := newRunsModel(t)
		swapFetch(t, func() ([]storage.RunRow, error) {
			t.Fatal("the fetch must not run without a declared remote")
			return nil, nil
		})
		if cmd := m.startRemoteRunsFetch(); cmd != nil {
			t.Error("startRemoteRunsFetch should return no command with no remotes")
		}
		if m.runsFetching {
			t.Error("runsFetching should stay false")
		}
		if !strings.Contains(m.toastMsg, "no remote runners") {
			t.Errorf("toast = %q, want it to name the missing registry", m.toastMsg)
		}
	})

	t.Run("f fetches and the row lands in the sorted list", func(t *testing.T) {
		m := newRunsModel(t)
		withRemoteConfig(t, m)
		swapFetch(t, func() ([]storage.RunRow, error) { return []storage.RunRow{remoteRow}, nil })

		res, cmd := m.updateRuns(keyRunes('f'))
		fetching := res.(Model)
		if !fetching.runsFetching {
			t.Error("runsFetching should be set while the fetch is in flight")
		}
		if !strings.Contains(stripANSI(fetching.viewRuns()), "fetching") {
			t.Error("the state line should show the fetch is running")
		}
		if cmd == nil {
			t.Fatal("f should return the fetch command")
		}
		msg, ok := cmd().(runsRemoteMsg)
		if !ok {
			t.Fatalf("fetch produced %T, want runsRemoteMsg", cmd())
		}
		fetching.applyRemoteRuns(msg)
		if fetching.runsFetching {
			t.Error("runsFetching should be cleared once the answer arrives")
		}
		out := stripANSI(fetching.viewRuns())
		if !strings.Contains(out, "runner/orbit") || !strings.Contains(out, "running 2/6") {
			t.Errorf("the remote row is missing from the table:\n%s", out)
		}
		if !strings.Contains(out, "1 row(s), fetched") {
			t.Errorf("the state line should carry the fetch's age:\n%s", out)
		}
		// The local rows are still there - the fetch adds a machine, it does not
		// replace the screen.
		if !strings.Contains(out, "p-9") {
			t.Errorf("local rows disappeared after a remote fetch:\n%s", out)
		}
	})

	t.Run("a failed fetch keeps the rows already fetched", func(t *testing.T) {
		m := newRunsModel(t)
		withRemoteConfig(t, m)
		m.runsRemoteRows = []storage.RunRow{remoteRow}
		m.runsFetchAt = time.Now()
		m.refreshRunsView()

		m.applyRemoteRuns(runsRemoteMsg{err: fmt.Errorf("unreachable: no route to host")})
		if len(m.runsRemoteRows) != 1 {
			t.Errorf("remote rows = %d, want the previous answer kept", len(m.runsRemoteRows))
		}
		if !strings.Contains(m.runsFetchErr, "no route to host") {
			t.Errorf("runsFetchErr = %q, want the failure recorded", m.runsFetchErr)
		}
		out := stripANSI(m.viewRuns())
		if !strings.Contains(out, "fetch failed") || !strings.Contains(out, "orbit-vps-7") {
			t.Errorf("a failed fetch must show the error AND the stale rows:\n%s", out)
		}
	})

	t.Run("a note row for an unreachable machine renders without a tracker", func(t *testing.T) {
		m := newRunsModel(t)
		m.runsRemoteRows = []storage.RunRow{{Remote: "runner", Note: "unreachable: no answer within 20s"}}
		m.refreshRunsView()
		out := stripANSI(m.viewRuns())
		if !strings.Contains(out, "unreachable") {
			t.Errorf("the note should take the title's place:\n%s", out)
		}
		// And it must not knock the cursor out of the list.
		for i, r := range m.runsRows {
			if r.Note != "" {
				m.runsCursor = i
			}
		}
		m.openRunsRow()
		if m.currentView == viewExecutor {
			t.Error("a placeholder row has nothing to open")
		}
	})
}

// The cursor follows the ROW, not the index: rows are ordered by activity, so a
// tick that reorders them under a plain index would leave enter opening a
// different tracker than the one highlighted.
func TestRunsCursorFollowsTheRowAcrossRefreshes(t *testing.T) {
	m := newRunsModel(t)
	runsTracker(t, m, "p", "p-20", 1)
	// Two run-states with distinct activity stamps, so the order is well-defined
	// and can be flipped.
	stateDir := m.store.ProjectDir("p")
	older := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	if err := storage.WriteRunState(stateDir, &storage.RunState{
		TaskID: "p-20", Project: "p", Kind: storage.RunKindEpic, Status: storage.RunStatusDone,
		Started: older, Updated: older, Subs: []storage.SubRun{{ID: "p-20-1", Status: "merged"}},
	}); err != nil {
		t.Fatal(err)
	}
	m.refreshRunsView()

	target := ""
	for i, r := range m.runsRows {
		if r.Tracker == "p-20" {
			m.runsCursor = i
			target = r.Tracker
		}
	}
	if target == "" {
		t.Fatal("no row for p-20")
	}
	before := m.runsCursor

	// p-9's run ticks over, which reorders the list.
	fresh := time.Now().UTC().Format(time.RFC3339)
	if err := storage.WriteRunState(stateDir, &storage.RunState{
		TaskID: "p-9", Project: "p", Kind: storage.RunKindEpic, Status: storage.RunStatusRunning,
		Started: fresh, Updated: fresh, Subs: []storage.SubRun{{ID: "p-9-1", Status: "merged"}},
	}); err != nil {
		t.Fatal(err)
	}
	m.refreshRunsView()

	got := m.selectedRunRow()
	if got == nil || got.Tracker != target {
		t.Fatalf("after a refresh the cursor sits on %+v, want the row for %s", got, target)
	}
	if m.runsCursor == before && len(m.runsRows) > 1 {
		t.Logf("index unchanged (%d) - the rows happened not to move", before)
	}
}
