package board

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// boardKeysStore is the fixture every board-key test runs over: two projects so
// the project-switching keys have somewhere to go, and in "acme" two todo tasks
// (the first one carrying a branch, links and a session, so the yank and links
// menus have something to list) plus one doing task.
func boardKeysStore(t *testing.T) *storage.Store {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	for _, slug := range []string{"acme", "orbit"} {
		if err := store.CreateProject(slug, &storage.Project{Name: slug}); err != nil {
			t.Fatal(err)
		}
	}
	tasks := []struct {
		project string
		meta    storage.TaskMeta
	}{
		{"acme", storage.TaskMeta{ID: "acme-1", Title: "First", Status: storage.StatusTodo, Branch: "feat/first",
			Links: map[string]string{"pr": "https://example.com/pr/1"}, Sessions: []string{"sess-1"}}},
		{"acme", storage.TaskMeta{ID: "acme-2", Title: "Second", Status: storage.StatusTodo}},
		{"acme", storage.TaskMeta{ID: "acme-3", Title: "Third", Status: storage.StatusDoing}},
		{"orbit", storage.TaskMeta{ID: "orbit-1", Title: "Other project", Status: storage.StatusTodo}},
	}
	for _, tk := range tasks {
		task := &storage.Task{Meta: tk.meta}
		task.Meta.Created = "2026-09-01"
		task.Meta.Updated = "2026-09-01T10:00:00+02:00"
		if err := store.AddTask(tk.project, task); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// boardKeysModel opens the board on project "acme" with the cursor on acme-1
// in the todo column.
func boardKeysModel(t *testing.T) Model {
	t.Helper()
	m := New(boardKeysStore(t), "acme")
	if got := m.selectedTask(); got == nil || got.Meta.ID != "acme-1" {
		t.Fatalf("fixture: cursor should sit on acme-1, got %v", got)
	}
	return m
}

// press feeds the keys to updateBoard one after another, the way the board's
// Update hands them over, and returns the model and the command of the last.
func press(t *testing.T, m Model, keys ...tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.updateBoard(k)
		m = next.(Model)
	}
	return m, cmd
}

func ctrlKey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func storedStatus(t *testing.T, m Model, id string) storage.TaskStatus {
	t.Helper()
	task, err := m.store.FindTask("acme", id)
	if err != nil {
		t.Fatalf("find %s: %v", id, err)
	}
	return task.Meta.Status
}

// TestUpdateBoardKeys pins what every key of the board's key switch does, so
// the switch can be restructured without a key changing its meaning.
func TestUpdateBoardKeys(t *testing.T) {
	// No clipboard tool on PATH: y must report that instead of writing to the
	// clipboard of whoever runs the tests.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("TMUX", "")

	cases := []struct {
		name  string
		setup func(*Model)
		keys  []tea.KeyMsg
		check func(t *testing.T, m Model, cmd tea.Cmd)
	}{
		{"q arms the quit confirmation", nil, []tea.KeyMsg{keyRunes('q')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "quit" || cmd != nil {
					t.Errorf("confirmAction = %q, cmd nil = %v", m.confirmAction, cmd == nil)
				}
			}},
		{"q twice quits", nil, []tea.KeyMsg{keyRunes('q'), keyRunes('q')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if cmd == nil {
					t.Fatal("no command")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Errorf("second q should return tea.Quit")
				}
			}},
		{"another key drops an armed confirmation and still acts", nil, []tea.KeyMsg{keyRunes('d'), keyRunes('j')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "" || m.confirmTaskID != "" {
					t.Errorf("confirmation survived: %q %q", m.confirmAction, m.confirmTaskID)
				}
				if m.cursors[0] != 1 {
					t.Errorf("j after the reset should still move: cursor = %d", m.cursors[0])
				}
			}},
		{"the confirming key keeps its confirmation for another task", nil, []tea.KeyMsg{keyRunes('d'), keyRunes('j'), keyRunes('d')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "done" || m.confirmTaskID != "acme-2" {
					t.Errorf("confirm = %q %q", m.confirmAction, m.confirmTaskID)
				}
				if storedStatus(t, m, "acme-1") != storage.StatusTodo {
					t.Error("acme-1 must not be done")
				}
			}},
		{"h wraps to the last column", nil, []tea.KeyMsg{keyRunes('h')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.activeCol != len(m.statuses)-1 {
					t.Errorf("activeCol = %d of %d", m.activeCol, len(m.statuses))
				}
			}},
		{"l moves to the next column", nil, []tea.KeyMsg{keyRunes('l')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.activeCol != 1 {
					t.Errorf("activeCol = %d", m.activeCol)
				}
			}},
		{"j moves down, a second j stops at the last task", nil, []tea.KeyMsg{keyRunes('j'), keyRunes('j')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.cursors[0] != 1 {
					t.Errorf("cursor = %d", m.cursors[0])
				}
			}},
		{"k moves up and stops at the top", nil, []tea.KeyMsg{keyRunes('j'), keyRunes('k'), keyRunes('k')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.cursors[0] != 0 {
					t.Errorf("cursor = %d", m.cursors[0])
				}
			}},
		{"G jumps to the bottom", nil, []tea.KeyMsg{keyRunes('G')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.cursors[0] != 1 {
					t.Errorf("cursor = %d", m.cursors[0])
				}
			}},
		{"g jumps to the top", nil, []tea.KeyMsg{keyRunes('G'), keyRunes('g')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.cursors[0] != 0 {
					t.Errorf("cursor = %d", m.cursors[0])
				}
			}},
		{"ctrl+d moves down, clamped to the last task", nil, []tea.KeyMsg{ctrlKey(tea.KeyCtrlD)},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.cursors[0] != 1 {
					t.Errorf("cursor = %d", m.cursors[0])
				}
			}},
		{"ctrl+u moves up, clamped to the top", nil, []tea.KeyMsg{ctrlKey(tea.KeyCtrlD), ctrlKey(tea.KeyCtrlU)},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.cursors[0] != 0 {
					t.Errorf("cursor = %d", m.cursors[0])
				}
			}},
		{"r refreshes", nil, []tea.KeyMsg{keyRunes('r')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.toastMsg != "Refreshed" {
					t.Errorf("toast = %q", m.toastMsg)
				}
			}},
		{"tab goes to the next project", nil, []tea.KeyMsg{ctrlKey(tea.KeyTab)},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.projects[m.activeProject] != "orbit" {
					t.Errorf("project = %q", m.projects[m.activeProject])
				}
			}},
		{"shift+tab goes to the previous project", nil, []tea.KeyMsg{ctrlKey(tea.KeyShiftTab)},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.projects[m.activeProject] != "all" {
					t.Errorf("project = %q", m.projects[m.activeProject])
				}
			}},
		{"m moves the task forward", nil, []tea.KeyMsg{keyRunes('m')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := storedStatus(t, m, "acme-1"); got != storage.StatusDoing {
					t.Errorf("status = %q", got)
				}
			}},
		{"M moves the task back", nil, []tea.KeyMsg{keyRunes('l'), keyRunes('M')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := storedStatus(t, m, "acme-3"); got != storage.StatusTodo {
					t.Errorf("status = %q", got)
				}
			}},
		{"enter opens the detail view", nil, []tea.KeyMsg{ctrlKey(tea.KeyEnter)},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewDetail || m.previousView != viewBoard || m.detailTask == nil || m.detailTask.Meta.ID != "acme-1" {
					t.Errorf("view = %v prev = %v task = %v", m.currentView, m.previousView, m.detailTask)
				}
			}},
		{"o opens the detail view", nil, []tea.KeyMsg{keyRunes('o')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewDetail {
					t.Errorf("view = %v", m.currentView)
				}
			}},
		{"space opens the detail view", nil, []tea.KeyMsg{{Type: tea.KeySpace, Runes: []rune{' '}}},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewDetail {
					t.Errorf("view = %v", m.currentView)
				}
			}},
		{"e opens the editor", nil, []tea.KeyMsg{keyRunes('e')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if cmd == nil {
					t.Error("e should return the editor command")
				}
			}},
		{"d arms done", nil, []tea.KeyMsg{keyRunes('d')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "done" || m.confirmTaskID != "acme-1" || storedStatus(t, m, "acme-1") != storage.StatusTodo {
					t.Errorf("confirm = %q %q", m.confirmAction, m.confirmTaskID)
				}
			}},
		{"d twice marks done", nil, []tea.KeyMsg{keyRunes('d'), keyRunes('d')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := storedStatus(t, m, "acme-1"); got != storage.StatusDone || m.confirmAction != "" {
					t.Errorf("status = %q confirm = %q", got, m.confirmAction)
				}
			}},
		{"w arms waiting", nil, []tea.KeyMsg{keyRunes('w')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "waiting" || m.confirmTaskID != "acme-1" {
					t.Errorf("confirm = %q %q", m.confirmAction, m.confirmTaskID)
				}
			}},
		{"w twice marks waiting", nil, []tea.KeyMsg{keyRunes('w'), keyRunes('w')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := storedStatus(t, m, "acme-1"); got != storage.StatusWaiting || m.confirmAction != "" {
					t.Errorf("status = %q confirm = %q", got, m.confirmAction)
				}
			}},
		{"X arms delete", nil, []tea.KeyMsg{keyRunes('X')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "delete" || m.confirmTaskID != "acme-1" {
					t.Errorf("confirm = %q %q", m.confirmAction, m.confirmTaskID)
				}
			}},
		{"X twice deletes and keeps an undo", nil, []tea.KeyMsg{keyRunes('X'), keyRunes('X')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if _, err := m.store.FindTask("acme", "acme-1"); err == nil {
					t.Error("acme-1 still exists")
				}
				if m.lastUndo == nil || m.lastUndo.kind != "delete" || m.confirmAction != "" {
					t.Errorf("undo = %+v confirm = %q", m.lastUndo, m.confirmAction)
				}
			}},
		{"y copies the id", nil, []tea.KeyMsg{keyRunes('y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !strings.HasPrefix(m.toastMsg, "copy failed") {
					t.Errorf("toast = %q", m.toastMsg)
				}
			}},
		{"y on a task with no id says so", func(m *Model) { m.selectedTask().Meta.ID = "" }, []tea.KeyMsg{keyRunes('y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.toastMsg != "no id set" {
					t.Errorf("toast = %q", m.toastMsg)
				}
			}},
		{"Y opens the yank menu", nil, []tea.KeyMsg{keyRunes('Y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				var labels []string
				for _, it := range m.yankItems {
					labels = append(labels, it.label)
				}
				if !m.yankMenu || strings.Join(labels, ",") != "branch,id,title,path,session,link: pr" {
					t.Errorf("yankMenu = %v items = %v", m.yankMenu, labels)
				}
			}},
		{"L opens the links menu", nil, []tea.KeyMsg{keyRunes('L')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.linksMenu || len(m.linkItems) != 1 || m.linkItems[0].url != "https://example.com/pr/1" {
					t.Errorf("linksMenu = %v items = %+v", m.linksMenu, m.linkItems)
				}
			}},
		{"L on a task without links does nothing", nil, []tea.KeyMsg{keyRunes('j'), keyRunes('L')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.linksMenu {
					t.Error("links menu opened with no links")
				}
			}},
		{"A arms archive", nil, []tea.KeyMsg{keyRunes('A')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "archive" || m.confirmTaskID != "acme-1" {
					t.Errorf("confirm = %q %q", m.confirmAction, m.confirmTaskID)
				}
			}},
		{"A twice archives", nil, []tea.KeyMsg{keyRunes('A'), keyRunes('A')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := storedStatus(t, m, "acme-1"); got != storage.StatusArchived || m.confirmAction != "" {
					t.Errorf("status = %q confirm = %q", got, m.confirmAction)
				}
			}},
		{"ctrl+a opens the archive and drops a confirmation", nil, []tea.KeyMsg{keyRunes('A'), ctrlKey(tea.KeyCtrlA)},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewArchive || m.archiveCursor != 0 || m.confirmAction != "" || m.confirmTaskID != "" {
					t.Errorf("view = %v confirm = %q", m.currentView, m.confirmAction)
				}
			}},
		{"i opens the project info", nil, []tea.KeyMsg{keyRunes('i')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewProjectInfo || m.previousView != viewBoard || m.infoSlug != "acme" || m.infoProject == nil {
					t.Errorf("view = %v slug = %q", m.currentView, m.infoSlug)
				}
			}},
		{"i in the all view takes the selected task's project", func(m *Model) { m.activeProject = 0; m.reload() }, []tea.KeyMsg{keyRunes('i')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewProjectInfo || m.infoSlug != m.selectedTask().Project {
					t.Errorf("view = %v slug = %q", m.currentView, m.infoSlug)
				}
			}},
		{"u undoes the last change", nil, []tea.KeyMsg{keyRunes('m'), keyRunes('u')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := storedStatus(t, m, "acme-1"); got != storage.StatusTodo || m.toastMsg != "undone: move" {
					t.Errorf("status = %q toast = %q", got, m.toastMsg)
				}
			}},
		{"u with nothing to undo says so", nil, []tea.KeyMsg{keyRunes('u')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.toastMsg != "nothing to undo" {
					t.Errorf("toast = %q", m.toastMsg)
				}
			}},
		{"V opens the column menu", nil, []tea.KeyMsg{keyRunes('V')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.colVisMenu || len(m.colVisItems) != len(storage.DefaultStatuses) {
					t.Fatalf("colVisMenu = %v items = %+v", m.colVisMenu, m.colVisItems)
				}
				for _, it := range m.colVisItems {
					if !it.visible {
						t.Errorf("%s should be visible", it.status)
					}
				}
			}},
		{"V in the all view lists every project's statuses", func(m *Model) { m.activeProject = 0; m.reload() }, []tea.KeyMsg{keyRunes('V')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.colVisMenu || len(m.colVisItems) != len(m.store.GetAllStatuses()) {
					t.Errorf("colVisMenu = %v items = %+v", m.colVisMenu, m.colVisItems)
				}
			}},
		{"v enters select mode with the task selected", nil, []tea.KeyMsg{keyRunes('v')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.selecting || !m.selected["acme-1"] {
					t.Errorf("selecting = %v selected = %v", m.selecting, m.selected)
				}
			}},
		{"ctrl+j moves the task down the column", nil, []tea.KeyMsg{ctrlKey(tea.KeyCtrlJ)},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if col := m.columnTasks(0); col[0].Meta.ID != "acme-2" || col[1].Meta.ID != "acme-1" {
					t.Errorf("order = %s,%s", col[0].Meta.ID, col[1].Meta.ID)
				}
			}},
		{"ctrl+k moves the task up the column", nil, []tea.KeyMsg{keyRunes('j'), ctrlKey(tea.KeyCtrlK)},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if col := m.columnTasks(0); col[0].Meta.ID != "acme-2" || col[1].Meta.ID != "acme-1" {
					t.Errorf("order = %s,%s", col[0].Meta.ID, col[1].Meta.ID)
				}
			}},
		{"; toggles zoom", nil, []tea.KeyMsg{keyRunes(';')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.zoomed {
					t.Error("not zoomed")
				}
			}},
		{"c opens the launch menu for claude", nil, []tea.KeyMsg{keyRunes('c')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.claudeMenu || m.launchAgent != launchAgentClaude || cmd != nil {
					t.Errorf("claudeMenu = %v agent = %v", m.claudeMenu, m.launchAgent)
				}
			}},
		{"x opens the launch menu for the executor", nil, []tea.KeyMsg{keyRunes('x')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.claudeMenu || m.launchAgent != launchAgentExecutor || cmd != nil {
					t.Errorf("claudeMenu = %v agent = %v", m.claudeMenu, m.launchAgent)
				}
			}},
		{"W with no run says so", nil, []tea.KeyMsg{keyRunes('W')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewBoard || m.toastMsg != "no executor run for this task (launch with x)" {
					t.Errorf("view = %v toast = %q", m.currentView, m.toastMsg)
				}
			}},
		{"W opens the run view of a task with a run", writeRun(storage.RunStatusDone), []tea.KeyMsg{keyRunes('W')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewExecutor || m.executorRunTaskID != "acme-1" {
					t.Errorf("view = %v run = %q", m.currentView, m.executorRunTaskID)
				}
			}},
		{"K with no live run says so", writeRun(storage.RunStatusDone), []tea.KeyMsg{keyRunes('K')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.toastMsg != "no live executor run to stop" || m.confirmAction != "" {
					t.Errorf("toast = %q confirm = %q", m.toastMsg, m.confirmAction)
				}
			}},
		{"K on a live run arms the kill", writeRun(storage.RunStatusRunning), []tea.KeyMsg{keyRunes('K')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "kill-run" || m.confirmTaskID != "acme-1" || m.confirmRunFinish || cmd != nil {
					t.Errorf("confirm = %q %q finish = %v", m.confirmAction, m.confirmTaskID, m.confirmRunFinish)
				}
			}},
		{"t adds the task to focus", nil, []tea.KeyMsg{keyRunes('t')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.focusPlan.Contains("acme-1") || m.toastMsg != "Added to focus" || !m.focusSet["acme-1"] {
					t.Errorf("focus = %+v toast = %q", m.focusPlan, m.toastMsg)
				}
			}},
		{"t twice removes it again", nil, []tea.KeyMsg{keyRunes('t'), keyRunes('t')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.focusPlan.Contains("acme-1") || m.toastMsg != "Removed from focus" {
					t.Errorf("focus = %+v toast = %q", m.focusPlan, m.toastMsg)
				}
			}},
		{"T opens the focus view", nil, []tea.KeyMsg{keyRunes('T')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewFocus || m.focusCursor != 0 {
					t.Errorf("view = %v", m.currentView)
				}
			}},
		{"R opens the runs view", nil, []tea.KeyMsg{keyRunes('R')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewRuns || m.runsPrevView != viewBoard {
					t.Errorf("view = %v prev = %v", m.currentView, m.runsPrevView)
				}
			}},
		{"? opens the help", nil, []tea.KeyMsg{keyRunes('?')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.showHelp {
					t.Error("help not shown")
				}
			}},
		{"a opens the add dialog", nil, []tea.KeyMsg{keyRunes('a')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.adding || m.addStep != 0 || m.addInput.Prompt != "Title: " || cmd == nil {
					t.Errorf("adding = %v step = %d prompt = %q", m.adding, m.addStep, m.addInput.Prompt)
				}
			}},
		{"a in the all view switches to the first project", func(m *Model) { m.activeProject = 0; m.reload() }, []tea.KeyMsg{keyRunes('a')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.activeProject != 1 || m.activeCol != 0 || !m.adding {
					t.Errorf("project = %d col = %d adding = %v", m.activeProject, m.activeCol, m.adding)
				}
			}},
		{"a in the all view with no project does nothing", func(m *Model) { m.projects = []string{"all"}; m.activeProject = 0 }, []tea.KeyMsg{keyRunes('a')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.adding || cmd != nil {
					t.Errorf("adding = %v", m.adding)
				}
			}},
		{"P opens the project picker", nil, []tea.KeyMsg{keyRunes('P')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.projectPicker {
					t.Error("picker not open")
				}
			}},
		{"/ opens the search", func(m *Model) { m.searchQuery = "fir" }, []tea.KeyMsg{keyRunes('/')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.searching || m.searchInput.Value() != "fir" || cmd == nil {
					t.Errorf("searching = %v value = %q", m.searching, m.searchInput.Value())
				}
			}},
		{"a digit jumps to that visible project", nil, []tea.KeyMsg{keyRunes('2')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.projects[m.activeProject] != m.visibleProjects()[1] {
					t.Errorf("project = %q", m.projects[m.activeProject])
				}
			}},
		{"a digit past the visible projects does nothing", nil, []tea.KeyMsg{keyRunes('9')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.projects[m.activeProject] != "acme" {
					t.Errorf("project = %q", m.projects[m.activeProject])
				}
			}},
		{"an unbound key does nothing", nil, []tea.KeyMsg{keyRunes('z')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewBoard || m.cursors[0] != 0 || cmd != nil {
					t.Errorf("view = %v cursor = %d", m.currentView, m.cursors[0])
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := boardKeysModel(t)
			if c.setup != nil {
				c.setup(&m)
			}
			m, cmd := press(t, m, c.keys...)
			c.check(t, m, cmd)
		})
	}
}

// writeRun gives acme-1 an executor run in the given status. A running one
// carries this test binary's pid, so it reads as live.
func writeRun(status string) func(*Model) {
	return func(m *Model) {
		st := &storage.RunState{TaskID: "acme-1", Project: "acme", Kind: storage.RunKindWork, Status: status,
			PID: os.Getpid(), Started: storage.Now(), Updated: storage.Now()}
		if err := storage.WriteRunState(m.store.ProjectDir("acme"), st); err != nil {
			panic(err)
		}
		m.refreshRunStates() // what the board's tick does
	}
}

// TestUpdateBoardTaskKeysOnAnEmptyColumn: every key that acts on the selected
// task is a no-op when the column is empty - no confirmation armed, no menu,
// no view change.
func TestUpdateBoardTaskKeysOnAnEmptyColumn(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, r := range []rune{'m', 'M', 'o', 'e', 'd', 'w', 'X', 'y', 'Y', 'L', 'A', 'v', 'c', 'x', 'W', 'K', 't'} {
		t.Run(string(r), func(t *testing.T) {
			m := boardKeysModel(t)
			m, _ = press(t, m, keyRunes('l'), keyRunes('l')) // waiting: empty
			if len(m.columnTasks(m.activeCol)) != 0 {
				t.Fatal("fixture: the waiting column should be empty")
			}
			m, cmd := press(t, m, keyRunes(r))
			if cmd != nil || m.confirmAction != "" || m.currentView != viewBoard || m.claudeMenu || m.yankMenu ||
				m.linksMenu || len(m.selected) != 0 || m.toastMsg != "" || len(m.focusPlan.Tasks) != 0 {
				t.Errorf("key %q acted on an empty column: view = %v confirm = %q toast = %q", r, m.currentView, m.confirmAction, m.toastMsg)
			}
		})
	}
}
