package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// detailKeysStore is the detail-view fixture: in "acme", a tracker with one
// child, and a plain task carrying everything the yank and links menus list.
func detailKeysStore(t *testing.T) *storage.Store {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("acme", &storage.Project{Name: "acme", Path: "/Users/alice/acme", Repo: "example.com/acme",
		Links: map[string]string{"docs": "https://example.com/docs"}}); err != nil {
		t.Fatal(err)
	}
	for _, meta := range []storage.TaskMeta{
		{ID: "acme-1", Title: "Plain", Status: storage.StatusTodo, Branch: "feat/plain", Brief: "where we are", AC: "it works",
			Links: map[string]string{"pr": "https://example.com/pr/1"}, Sessions: []string{"sess-1"}},
		{ID: "acme-2", Title: "Tracker", Status: storage.StatusTodo},
		{ID: "acme-3", Title: "Child", Status: storage.StatusTodo, Parent: "acme-2"},
	} {
		task := &storage.Task{Meta: meta, Body: "body of " + meta.ID}
		task.Meta.Created = "2026-09-01"
		task.Meta.Updated = "2026-09-01T10:00:00+02:00"
		if err := store.AddTask("acme", task); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// detailKeysModel opens the detail view of task id from the board.
func detailKeysModel(t *testing.T, id string) Model {
	t.Helper()
	m := New(detailKeysStore(t), "acme")
	var task *storage.Task
	for _, tk := range m.tasks {
		if tk.Meta.ID == id {
			task = tk
		}
	}
	if task == nil {
		t.Fatalf("fixture: no task %s", id)
	}
	m.previousView = viewBoard
	m.currentView = viewDetail
	m.openDetailTask(task)
	return m
}

// projectInfoModel opens the project-info view of "acme" from the board.
func projectInfoModel(t *testing.T) Model {
	t.Helper()
	m := New(detailKeysStore(t), "acme")
	proj, err := m.store.GetProject("acme")
	if err != nil {
		t.Fatal(err)
	}
	m.previousView = viewBoard
	m.currentView = viewProjectInfo
	m.infoProject = proj
	m.infoSlug = "acme"
	m.detailViewport = viewport.New(m.width, m.height-2)
	m.detailViewport.SetContent(longContent())
	return m
}

func longContent() string { return strings.Repeat("line\n", 100) }

func pressDetail(t *testing.T, m Model, keys ...tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var next tea.Model
		next, cmd = m.updateDetail(k)
		m = next.(Model)
	}
	return m, cmd
}

func detailStatus(t *testing.T, m Model, id string) storage.TaskStatus {
	t.Helper()
	task, err := m.store.FindTask("acme", id)
	if err != nil {
		t.Fatalf("find %s: %v", id, err)
	}
	return task.Meta.Status
}

func detailRun(status string) func(*Model) {
	return func(m *Model) {
		st := &storage.RunState{TaskID: "acme-1", Project: "acme", Kind: storage.RunKindWork, Status: status,
			PID: os.Getpid(), Started: storage.Now(), Updated: storage.Now()}
		if err := storage.WriteRunState(m.store.ProjectDir("acme"), st); err != nil {
			panic(err)
		}
		m.refreshRunStates()
	}
}

func space() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}} }

// TestUpdateDetailProjectInfoKeys pins the project-info half of updateDetail.
func TestUpdateDetailProjectInfoKeys(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("TMUX", "")
	cases := []struct {
		name  string
		setup func(*Model)
		keys  []tea.KeyMsg
		check func(t *testing.T, m Model, cmd tea.Cmd)
	}{
		{"esc goes back", nil, []tea.KeyMsg{{Type: tea.KeyEsc}}, backToBoard},
		{"q goes back", nil, []tea.KeyMsg{keyRunes('q')}, backToBoard},
		{"o goes back", nil, []tea.KeyMsg{keyRunes('o')}, backToBoard},
		{"space goes back", nil, []tea.KeyMsg{space()}, backToBoard},
		{"y copies the slug", nil, []tea.KeyMsg{keyRunes('y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !strings.HasPrefix(m.toastMsg, "copy failed") || m.currentView != viewProjectInfo {
					t.Errorf("toast = %q view = %v", m.toastMsg, m.currentView)
				}
			}},
		{"y with no slug does nothing", func(m *Model) { m.infoSlug = "" }, []tea.KeyMsg{keyRunes('y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.toastMsg != "" || m.detailViewport.YOffset != 0 {
					t.Errorf("toast = %q offset = %d", m.toastMsg, m.detailViewport.YOffset)
				}
			}},
		{"Y opens the yank menu", nil, []tea.KeyMsg{keyRunes('Y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := yankLabels(m); !m.yankMenu || got != "slug,path,repo,link: docs" {
					t.Errorf("yankMenu = %v items = %s", m.yankMenu, got)
				}
			}},
		{"Y with no project does nothing", func(m *Model) { m.infoProject = nil }, []tea.KeyMsg{keyRunes('Y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.yankMenu || m.detailViewport.YOffset != 0 {
					t.Errorf("yankMenu = %v", m.yankMenu)
				}
			}},
		{"L opens the links menu", nil, []tea.KeyMsg{keyRunes('L')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.linksMenu || len(m.linkItems) != 1 || m.linkItems[0].name != "docs" {
					t.Errorf("linksMenu = %v items = %+v", m.linksMenu, m.linkItems)
				}
			}},
		{"L with no links does nothing", func(m *Model) { m.infoProject.Links = nil }, []tea.KeyMsg{keyRunes('L')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.linksMenu {
					t.Error("links menu opened")
				}
			}},
		{"c opens the project launch menu", nil, []tea.KeyMsg{keyRunes('c')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.claudeMenu || !m.projectScopeLaunch || m.projectScopeSlug != "acme" {
					t.Errorf("claudeMenu = %v scope = %v %q", m.claudeMenu, m.projectScopeLaunch, m.projectScopeSlug)
				}
			}},
		{"c with no slug does nothing", func(m *Model) { m.infoSlug = "" }, []tea.KeyMsg{keyRunes('c')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.claudeMenu {
					t.Error("launch menu opened")
				}
			}},
		{"any other key scrolls", nil, []tea.KeyMsg{keyRunes('j')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.detailViewport.YOffset != 1 || m.currentView != viewProjectInfo {
					t.Errorf("offset = %d view = %v", m.detailViewport.YOffset, m.currentView)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := projectInfoModel(t)
			if c.setup != nil {
				c.setup(&m)
			}
			m, cmd := pressDetail(t, m, c.keys...)
			c.check(t, m, cmd)
		})
	}
}

func backToBoard(t *testing.T, m Model, cmd tea.Cmd) {
	t.Helper()
	if m.currentView != viewBoard || cmd != nil {
		t.Errorf("view = %v cmd nil = %v", m.currentView, cmd == nil)
	}
}

func yankLabels(m Model) string {
	var labels []string
	for _, it := range m.yankItems {
		labels = append(labels, it.label)
	}
	return strings.Join(labels, ",")
}

// TestUpdateDetailTaskKeys pins every key of the task-detail half of
// updateDetail.
func TestUpdateDetailTaskKeys(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("TMUX", "")
	t.Setenv("HOME", t.TempDir())
	withMatches := func(m *Model) {
		m.detailTask.Body = longContent() // the highlights re-render the task
		m.detailViewport.SetContent(longContent())
		m.detailSearchQuery = "line"
		m.detailSearchMatches = []int{3, 5, 7}
		m.detailSearchIdx = 0
	}
	noTask := func(m *Model) {
		m.detailTask = nil
		m.detailViewport.SetContent(longContent())
	}
	scrolled := func(t *testing.T, m Model, cmd tea.Cmd) {
		t.Helper()
		if m.detailViewport.YOffset != 1 || m.currentView != viewDetail {
			t.Errorf("offset = %d view = %v", m.detailViewport.YOffset, m.currentView)
		}
	}
	untouched := func(t *testing.T, m Model, cmd tea.Cmd) {
		t.Helper()
		if m.currentView != viewDetail || cmd != nil || m.detailViewport.YOffset != 0 || m.toastMsg != "" ||
			m.claudeMenu || m.yankMenu || m.linksMenu || m.sessionMenu || m.confirmAction != "" {
			t.Errorf("view = %v offset = %d toast = %q confirm = %q", m.currentView, m.detailViewport.YOffset, m.toastMsg, m.confirmAction)
		}
	}
	cases := []struct {
		name  string
		task  string
		setup func(*Model)
		keys  []tea.KeyMsg
		check func(t *testing.T, m Model, cmd tea.Cmd)
	}{
		{"search mode owns the keyboard", "acme-1", func(m *Model) { m.detailSearching = true }, []tea.KeyMsg{{Type: tea.KeyEsc}},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.detailSearching || m.currentView != viewDetail {
					t.Errorf("searching = %v view = %v", m.detailSearching, m.currentView)
				}
			}},
		{"esc clears an active search first", "acme-1", withMatches, []tea.KeyMsg{{Type: tea.KeyEsc}},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.detailSearchQuery != "" || m.detailSearchMatches != nil || m.currentView != viewDetail {
					t.Errorf("query = %q matches = %v view = %v", m.detailSearchQuery, m.detailSearchMatches, m.currentView)
				}
			}},
		{"esc with no search goes back", "acme-1", nil, []tea.KeyMsg{{Type: tea.KeyEsc}}, backToBoard},
		{"q goes back", "acme-1", nil, []tea.KeyMsg{keyRunes('q')}, backToBoard},
		{"o goes back", "acme-1", nil, []tea.KeyMsg{keyRunes('o')}, backToBoard},
		{"space goes back", "acme-1", nil, []tea.KeyMsg{space()}, backToBoard},
		{"/ starts a search", "acme-1", func(m *Model) { m.detailSearchQuery = "old" }, []tea.KeyMsg{keyRunes('/')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.detailSearching || m.detailSearchInput.Value() != "old" || cmd == nil {
					t.Errorf("searching = %v value = %q", m.detailSearching, m.detailSearchInput.Value())
				}
			}},
		{"n goes to the next match", "acme-1", withMatches, []tea.KeyMsg{keyRunes('n')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.detailSearchIdx != 1 || m.detailViewport.YOffset != 5 {
					t.Errorf("idx = %d offset = %d", m.detailSearchIdx, m.detailViewport.YOffset)
				}
			}},
		{"N wraps to the last match", "acme-1", withMatches, []tea.KeyMsg{keyRunes('N')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.detailSearchIdx != 2 || m.detailViewport.YOffset != 7 {
					t.Errorf("idx = %d offset = %d", m.detailSearchIdx, m.detailViewport.YOffset)
				}
			}},
		{"n with no matches does nothing", "acme-1", func(m *Model) { m.detailViewport.SetContent(longContent()) }, []tea.KeyMsg{keyRunes('n')}, untouched},
		{"N with no matches does nothing", "acme-1", func(m *Model) { m.detailViewport.SetContent(longContent()) }, []tea.KeyMsg{keyRunes('N')}, untouched},
		{"p on a child opens its parent", "acme-3", nil, []tea.KeyMsg{keyRunes('p')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.detailTask == nil || m.detailTask.Meta.ID != "acme-2" {
					t.Errorf("detail task = %v", m.detailTask)
				}
			}},
		{"p on a tracker opens the subtask picker", "acme-2", nil, []tea.KeyMsg{keyRunes('p')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.subtaskPicker || m.detailTask.Meta.ID != "acme-2" {
					t.Errorf("picker = %v", m.subtaskPicker)
				}
			}},
		{"p on a plain task does nothing", "acme-1", func(m *Model) { m.detailViewport.SetContent(longContent()) }, []tea.KeyMsg{keyRunes('p')}, untouched},
		{"p with no task scrolls like any key", "acme-1", noTask, []tea.KeyMsg{keyRunes('j'), keyRunes('p')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				// j scrolls one line; p falls through to the viewport, where it
				// is no binding, so the offset stays where j left it.
				if m.detailViewport.YOffset != 1 || m.subtaskPicker {
					t.Errorf("offset = %d", m.detailViewport.YOffset)
				}
			}},
		{"y copies the id", "acme-1", nil, []tea.KeyMsg{keyRunes('y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !strings.HasPrefix(m.toastMsg, "copy failed") {
					t.Errorf("toast = %q", m.toastMsg)
				}
			}},
		{"y with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('y')}, untouched},
		{"Y opens the yank menu", "acme-1", nil, []tea.KeyMsg{keyRunes('Y')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := yankLabels(m); !m.yankMenu || got != "branch,id,title,path,session,brief,ac,body,link: pr" {
					t.Errorf("yankMenu = %v items = %s", m.yankMenu, got)
				}
			}},
		{"Y with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('Y')}, untouched},
		{"e opens the editor", "acme-1", nil, []tea.KeyMsg{keyRunes('e')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if cmd == nil {
					t.Error("no editor command")
				}
			}},
		{"e with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('e')}, untouched},
		{"L opens the links menu", "acme-1", nil, []tea.KeyMsg{keyRunes('L')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.linksMenu || len(m.linkItems) != 1 || m.linkItems[0].name != "pr" {
					t.Errorf("linksMenu = %v items = %+v", m.linksMenu, m.linkItems)
				}
			}},
		{"L on a task without links does nothing", "acme-2", func(m *Model) { m.detailViewport.SetContent(longContent()) }, []tea.KeyMsg{keyRunes('L')}, untouched},
		{"m moves forward and goes back", "acme-1", nil, []tea.KeyMsg{keyRunes('m')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := detailStatus(t, m, "acme-1"); got != storage.StatusDoing || m.currentView != viewBoard {
					t.Errorf("status = %q view = %v", got, m.currentView)
				}
			}},
		{"m with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('m')}, untouched},
		{"M moves back and goes back", "acme-1", func(m *Model) {
			m.doMoveForward(m.detailTask)
			m.currentView = viewDetail
		}, []tea.KeyMsg{keyRunes('M')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := detailStatus(t, m, "acme-1"); got != storage.StatusTodo || m.currentView != viewBoard {
					t.Errorf("status = %q view = %v", got, m.currentView)
				}
			}},
		{"M with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('M')}, untouched},
		{"d arms done", "acme-1", nil, []tea.KeyMsg{keyRunes('d')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "done" || m.confirmTaskID != "acme-1" || m.currentView != viewDetail {
					t.Errorf("confirm = %q %q view = %v", m.confirmAction, m.confirmTaskID, m.currentView)
				}
			}},
		{"d twice marks done and goes back", "acme-1", nil, []tea.KeyMsg{keyRunes('d'), keyRunes('d')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := detailStatus(t, m, "acme-1"); got != storage.StatusDone || m.currentView != viewBoard || m.confirmAction != "" {
					t.Errorf("status = %q view = %v confirm = %q", got, m.currentView, m.confirmAction)
				}
			}},
		{"w arms waiting", "acme-1", nil, []tea.KeyMsg{keyRunes('w')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "waiting" || m.confirmTaskID != "acme-1" {
					t.Errorf("confirm = %q %q", m.confirmAction, m.confirmTaskID)
				}
			}},
		{"w twice marks waiting and goes back", "acme-1", nil, []tea.KeyMsg{keyRunes('w'), keyRunes('w')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := detailStatus(t, m, "acme-1"); got != storage.StatusWaiting || m.currentView != viewBoard || m.confirmAction != "" {
					t.Errorf("status = %q view = %v", got, m.currentView)
				}
			}},
		{"A arms archive", "acme-1", nil, []tea.KeyMsg{keyRunes('A')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "archive" || m.confirmTaskID != "acme-1" {
					t.Errorf("confirm = %q %q", m.confirmAction, m.confirmTaskID)
				}
			}},
		{"A twice archives and goes back", "acme-1", nil, []tea.KeyMsg{keyRunes('A'), keyRunes('A')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if got := detailStatus(t, m, "acme-1"); got != storage.StatusArchived || m.currentView != viewBoard || m.confirmAction != "" {
					t.Errorf("status = %q view = %v", got, m.currentView)
				}
			}},
		{"d, w, A with no task do nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('d'), keyRunes('w'), keyRunes('A')}, untouched},
		{"c opens the launch menu", "acme-1", nil, []tea.KeyMsg{keyRunes('c')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.claudeMenu || m.launchAgent != launchAgentClaude {
					t.Errorf("claudeMenu = %v agent = %v", m.claudeMenu, m.launchAgent)
				}
			}},
		{"x opens the executor menu", "acme-1", nil, []tea.KeyMsg{keyRunes('x')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.claudeMenu || m.launchAgent != launchAgentExecutor {
					t.Errorf("claudeMenu = %v agent = %v", m.claudeMenu, m.launchAgent)
				}
			}},
		{"c, x with no task do nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('c'), keyRunes('x')}, untouched},
		{"W with no run says so", "acme-1", nil, []tea.KeyMsg{keyRunes('W')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.toastMsg != "no executor run for this task (launch with x)" || m.currentView != viewDetail {
					t.Errorf("toast = %q", m.toastMsg)
				}
			}},
		{"W with a run opens it", "acme-1", detailRun(storage.RunStatusDone), []tea.KeyMsg{keyRunes('W')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewExecutor || m.executorPrevView != viewDetail {
					t.Errorf("view = %v prev = %v", m.currentView, m.executorPrevView)
				}
			}},
		{"W with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('W')}, untouched},
		{"F with no report says so", "acme-1", nil, []tea.KeyMsg{keyRunes('F')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !strings.HasPrefix(m.toastMsg, "no acceptance report for acme-1") || m.currentView != viewDetail {
					t.Errorf("toast = %q", m.toastMsg)
				}
			}},
		{"F opens the task's report", "acme-1", func(m *Model) {
			path := storage.FinishReportPath(m.store.ProjectDir("acme"), "acme-1")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				panic(err)
			}
			if err := os.WriteFile(path, []byte("# report\n"), 0o644); err != nil {
				panic(err)
			}
		}, []tea.KeyMsg{keyRunes('F')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.currentView != viewReport || m.reportTaskID != "acme-1" || m.reportPrevView != viewDetail {
					t.Errorf("view = %v report = %q", m.currentView, m.reportTaskID)
				}
			}},
		{"F with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('F')}, untouched},
		{"K with no live run says so", "acme-1", nil, []tea.KeyMsg{keyRunes('K')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.toastMsg != "no live executor run to stop" || m.confirmAction != "" {
					t.Errorf("toast = %q", m.toastMsg)
				}
			}},
		{"K on a live run arms the kill", "acme-1", detailRun(storage.RunStatusRunning), []tea.KeyMsg{keyRunes('K')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.confirmAction != "kill-run" || m.confirmTaskID != "acme-1" || m.confirmRunFinish || cmd != nil {
					t.Errorf("confirm = %q %q", m.confirmAction, m.confirmTaskID)
				}
			}},
		{"K with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('K')}, untouched},
		{"s opens the session menu", "acme-1", nil, []tea.KeyMsg{keyRunes('s')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if !m.sessionMenu || len(m.sessionMenuItems) != 1 || m.sessionMenuItems[0].sessionID != "sess-1" {
					t.Errorf("sessionMenu = %v items = %+v", m.sessionMenu, m.sessionMenuItems)
				}
			}},
		{"s with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('s')}, untouched},
		{"r re-reads the task from disk", "acme-1", func(m *Model) {
			fresh, err := m.store.FindTask("acme", "acme-1")
			if err != nil {
				panic(err)
			}
			fresh.Meta.Title = "Renamed on disk"
			if err := m.store.WriteTask(fresh); err != nil {
				panic(err)
			}
		}, []tea.KeyMsg{keyRunes('r')},
			func(t *testing.T, m Model, cmd tea.Cmd) {
				if m.detailTask.Meta.Title != "Renamed on disk" || m.toastMsg != "Refreshed" || !strings.Contains(m.detailPlainContent, "Renamed on disk") {
					t.Errorf("title = %q toast = %q", m.detailTask.Meta.Title, m.toastMsg)
				}
			}},
		{"r with no task does nothing", "acme-1", noTask, []tea.KeyMsg{keyRunes('r')}, untouched},
		{"any other key scrolls", "acme-1", func(m *Model) { m.detailViewport.SetContent(longContent()) }, []tea.KeyMsg{keyRunes('j')}, scrolled},
		{"any other key scrolls with no task", "acme-1", noTask, []tea.KeyMsg{keyRunes('j')}, scrolled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := detailKeysModel(t, c.task)
			if c.setup != nil {
				c.setup(&m)
			}
			m, cmd := pressDetail(t, m, c.keys...)
			c.check(t, m, cmd)
		})
	}
}
