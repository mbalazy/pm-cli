package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// The way back from a count to the thing it counted. `pm finish` writes a
// markdown report beside its run-state, and until F there was no way to reach it
// from the board at all - the number of open visual claims was the whole of what
// a human got.

// writeReport puts an acceptance report on disk where storage says it belongs.
func writeReport(t *testing.T, m *Model, taskID, body string) string {
	t.Helper()
	path := storage.FinishReportPath(m.store.ProjectDir("p"), taskID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFOpensTheAcceptanceReportInTheBoard(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	writeReport(t, m, "p-9", "# Acceptance\n\nThree screens still need looking at.\n")

	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-9"))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	v := next.(Model)
	m = &v

	if m.currentView != viewReport {
		t.Fatalf("view = %v, want the report view", m.currentView)
	}
	out := stripANSI(m.viewReport())
	for _, want := range []string{"Acceptance report · p-9", "Three screens still need looking at", "e: $EDITOR"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report view is missing %q:\n%s", want, out)
		}
	}

	// esc comes back to the view F was pressed from, not to the board - the
	// same rule the agent-view and the Runs list follow.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := next.(Model).currentView; got != viewDetail {
		t.Errorf("esc left the report in view %v, want the detail view it came from", got)
	}
}

// The second way in: e hands the file to $EDITOR. It is a tea.ExecProcess, so
// what is asserted here is that the key produces a command at all - the board
// cannot run an editor in a test.
func TestTheReportViewCanHandTheFileToTheEditor(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	path := writeReport(t, m, "p-9", "# Acceptance\n")
	if !m.openFinishReport("p", "p-9") {
		t.Fatal("the report did not open")
	}
	if m.reportPath != path {
		t.Errorf("reportPath = %q, want %q", m.reportPath, path)
	}
	_, cmd := m.updateReportView(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if cmd == nil {
		t.Error("e must launch $EDITOR on the report")
	}
}

// A tracker nobody has accepted has no report, and that is the ordinary state of
// most trackers - so it gets a sentence, not an empty pager and not an editor
// opened on a file that does not exist.
func TestFWithoutAReportSaysSoAndStaysPut(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-9"))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	v := next.(Model)
	m = &v
	if m.currentView != viewDetail {
		t.Fatalf("view = %v, want to have stayed in the detail view", m.currentView)
	}
	if !strings.Contains(m.toastMsg, "no acceptance report") {
		t.Errorf("toast = %q, want it to say there is no report", m.toastMsg)
	}
}

// An empty file is not the same as a missing one, and a blank pager reads as a
// broken board.
func TestAnEmptyReportSaysItIsEmpty(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	writeReport(t, m, "p-9", "   \n")
	if !m.openFinishReport("p", "p-9") {
		t.Fatal("an empty report still opens - it exists")
	}
	if out := stripANSI(m.viewReport()); !strings.Contains(out, "empty") {
		t.Errorf("the view says nothing about the file being empty:\n%s", out)
	}
}

// The report is re-read on r: an acceptance can be running while its previous
// report is on screen, and it rewrites the file when it ends.
func TestTheReportReloadsFromDisk(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	writeReport(t, m, "p-9", "# First pass\n")
	if !m.openFinishReport("p", "p-9") {
		t.Fatal("the report did not open")
	}
	writeReport(t, m, "p-9", "# Second pass\n")
	next, _ := m.updateReportView(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if out := stripANSI(next.(Model).viewReport()); !strings.Contains(out, "Second pass") {
		t.Errorf("r did not re-read the file:\n%s", out)
	}
}

// The same key in the Runs view, where the count that sends people looking for
// the report is actually printed.
func TestFInTheRunsViewOpensTheRowsReport(t *testing.T) {
	m := newBoardModel(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}},
		&storage.Task{Meta: storage.TaskMeta{ID: "p-9-1", Title: "Sub one", Status: storage.StatusTodo, Parent: "p-9"}},
	)
	writeReport(t, m, "p-9", "# Acceptance of the batch\n")
	m.openRunsView()
	if len(m.runsRows) == 0 {
		t.Fatal("the runs view has no rows to stand on")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	v := next.(Model)
	m = &v
	if m.currentView != viewReport {
		t.Fatalf("view = %v, want the report view", m.currentView)
	}
	if out := stripANSI(m.viewReport()); !strings.Contains(out, "Acceptance of the batch") {
		t.Errorf("the wrong report opened:\n%s", out)
	}
	// And back to the list, not to the board.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := next.(Model).currentView; got != viewRuns {
		t.Errorf("esc left the report in view %v, want the Runs list", got)
	}
}

// A remote row's report is a file on the other machine. The board says where it
// is rather than opening nothing.
func TestFOnARemoteRowSaysWhereTheReportLives(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	m.openRunsView()
	m.runsRows = []storage.RunRow{{Project: "p", Tracker: "p-9", Remote: "runner"}}
	m.runsCursor = 0

	m.openRowReport()
	if m.currentView == viewReport {
		t.Fatal("a remote row's report cannot be read from here")
	}
	if !strings.Contains(m.toastMsg, "runner") {
		t.Errorf("toast = %q, want it to name the machine", m.toastMsg)
	}
}

// Both footers name the key. F has no other trace on screen: nothing about a
// finished acceptance suggests there is a document behind it.
func TestBothViewsAdvertiseTheReportKey(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-9", Title: "Batch tracker", Status: storage.StatusDoing}})
	m.currentView = viewDetail
	m.openDetailTask(m.taskByID("p-9"))
	if out := stripANSI(m.viewDetail()); !strings.Contains(out, "F: report") {
		t.Errorf("the detail help bar does not name F:\n%s", out)
	}
	m.openRunsView()
	if out := stripANSI(m.viewRuns()); !strings.Contains(out, "F report") {
		t.Errorf("the runs footer does not name F:\n%s", out)
	}
}
