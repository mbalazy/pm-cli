package board

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
)

func makeTasks() []*storage.Task {
	return []*storage.Task{
		{Meta: storage.TaskMeta{ID: "a-1", Title: "Todo task", Status: storage.StatusTodo, Updated: "2025-01-03"}},
		{Meta: storage.TaskMeta{ID: "a-2", Title: "Doing task", Status: storage.StatusDoing, Updated: "2025-01-02"}},
		{Meta: storage.TaskMeta{ID: "a-3", Title: "Done task", Status: storage.StatusDone, Updated: "2025-01-01"}},
		{Meta: storage.TaskMeta{ID: "a-4", Title: "Archived task", Status: storage.StatusArchived, Updated: "2025-01-04"}},
		{Meta: storage.TaskMeta{ID: "a-5", Title: "Another todo", Status: storage.StatusTodo, Updated: "2025-01-05"}},
	}
}

func TestFilteredTasks(t *testing.T) {
	m := Model{
		tasks:    makeTasks(),
		statuses: []storage.TaskStatus{storage.StatusTodo, storage.StatusDoing, storage.StatusDone},
	}

	t.Run("filters by status", func(t *testing.T) {
		todos := m.filteredTasks(storage.StatusTodo)
		if len(todos) != 2 {
			t.Errorf("got %d todo tasks, want 2", len(todos))
		}
	})

	t.Run("excludes archived", func(t *testing.T) {
		// Even if we ask for archived status, filteredTasks explicitly skips archived
		archived := m.filteredTasks(storage.StatusArchived)
		if len(archived) != 0 {
			t.Errorf("got %d archived tasks, want 0 (filteredTasks excludes archived)", len(archived))
		}
	})

	t.Run("sorted by updated desc", func(t *testing.T) {
		todos := m.filteredTasks(storage.StatusTodo)
		if len(todos) < 2 {
			t.Fatal("need at least 2 todos")
		}
		if todos[0].Meta.Updated < todos[1].Meta.Updated {
			t.Error("tasks not sorted by updated desc")
		}
	})

	t.Run("search filters by title", func(t *testing.T) {
		m2 := Model{
			tasks:       makeTasks(),
			statuses:    []storage.TaskStatus{storage.StatusTodo},
			searchQuery: "another",
		}
		todos := m2.filteredTasks(storage.StatusTodo)
		if len(todos) != 1 {
			t.Errorf("got %d tasks, want 1 matching 'another'", len(todos))
		}
		if todos[0].Meta.ID != "a-5" {
			t.Errorf("got %q, want a-5", todos[0].Meta.ID)
		}
	})

	t.Run("search filters by ID", func(t *testing.T) {
		m2 := Model{
			tasks:       makeTasks(),
			statuses:    []storage.TaskStatus{storage.StatusTodo},
			searchQuery: "a-1",
		}
		todos := m2.filteredTasks(storage.StatusTodo)
		if len(todos) != 1 {
			t.Errorf("got %d tasks, want 1 matching id 'a-1'", len(todos))
		}
	})
}

func TestSnapshotTask(t *testing.T) {
	t.Run("deep copy links", func(t *testing.T) {
		orig := &storage.Task{
			Meta: storage.TaskMeta{
				ID:    "t-1",
				Title: "Test",
				Links: map[string]string{"pr": "https://github.com/pr/1"},
			},
			Body: "original body",
		}

		snap := snapshotTask(orig)

		// Modify original
		orig.Meta.Links["jira"] = "SCRUM-1"
		orig.Body = "modified body"

		// Snapshot should be unaffected
		if _, ok := snap.Meta.Links["jira"]; ok {
			t.Error("snapshot links should not be affected by original modification")
		}
		if snap.Meta.Links["pr"] != "https://github.com/pr/1" {
			t.Error("snapshot should preserve original link value")
		}
	})

	t.Run("deep copy tags", func(t *testing.T) {
		orig := &storage.Task{
			Meta: storage.TaskMeta{
				ID:   "t-2",
				Tags: []string{"backend", "urgent"},
			},
		}

		snap := snapshotTask(orig)
		orig.Meta.Tags[0] = "frontend"

		if snap.Meta.Tags[0] != "backend" {
			t.Errorf("snapshot tags should not be affected, got %q", snap.Meta.Tags[0])
		}
	})
}

func TestCardHeight(t *testing.T) {
	t.Run("no tags", func(t *testing.T) {
		task := &storage.Task{Meta: storage.TaskMeta{Title: "Test"}}
		h := cardHeight(task)
		// 2 lines (title + project) + 3 (border + margin) = 5
		if h != 5 {
			t.Errorf("cardHeight = %d, want 5", h)
		}
	})

	t.Run("with tags", func(t *testing.T) {
		task := &storage.Task{Meta: storage.TaskMeta{Title: "Test", Tags: []string{"backend"}}}
		h := cardHeight(task)
		// 3 lines (title + project + tags) + 3 = 6
		if h != 6 {
			t.Errorf("cardHeight = %d, want 6", h)
		}
	})

	t.Run("empty tags slice", func(t *testing.T) {
		task := &storage.Task{Meta: storage.TaskMeta{Title: "Test", Tags: []string{}}}
		h := cardHeight(task)
		if h != 5 {
			t.Errorf("cardHeight = %d, want 5 (empty tags = no extra line)", h)
		}
	})
}

func keyMsg(key string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func TestOpenClaudeMenu(t *testing.T) {
	task := &storage.Task{
		Meta: storage.TaskMeta{
			ID:    "p-1",
			Title: "Test task",
			Links: map[string]string{},
		},
	}

	t.Run("basic items without tmux", func(t *testing.T) {
		os.Unsetenv("TMUX")
		m := Model{}
		m.openClaudeMenu(task)

		if !m.claudeMenu {
			t.Error("claudeMenu should be true")
		}
		if m.claudeMenuSkipPerms {
			t.Error("skipPerms should default to false")
		}
		if m.claudeMenuCursor != 0 {
			t.Errorf("cursor = %d, want 0 (default to 'here' without tmux)", m.claudeMenuCursor)
		}

		kinds := make([]string, len(m.claudeMenuItems))
		for i, item := range m.claudeMenuItems {
			kinds[i] = item.kind
		}
		// Without tmux: here, worktree
		if len(kinds) != 2 || kinds[0] != "here" || kinds[1] != "worktree" {
			t.Errorf("items = %v, want [here worktree]", kinds)
		}
	})

	t.Run("tmux items when TMUX set", func(t *testing.T) {
		os.Setenv("TMUX", "/tmp/tmux-1000/default,12345,0")
		defer os.Unsetenv("TMUX")

		m := Model{}
		m.openClaudeMenu(task)

		kinds := make([]string, len(m.claudeMenuItems))
		for i, item := range m.claudeMenuItems {
			kinds[i] = item.kind
		}
		// With tmux: here, tmux, worktree, worktree-tmux
		expected := []string{"here", "tmux", "worktree", "worktree-tmux"}
		if len(kinds) != len(expected) {
			t.Fatalf("items = %v, want %v", kinds, expected)
		}
		for i, k := range expected {
			if kinds[i] != k {
				t.Errorf("item[%d] = %q, want %q", i, kinds[i], k)
			}
		}

		// Cursor should default to tmux option
		if m.claudeMenuItems[m.claudeMenuCursor].kind != "tmux" {
			t.Errorf("cursor at %q, want tmux", m.claudeMenuItems[m.claudeMenuCursor].kind)
		}
	})

	t.Run("resume items with session", func(t *testing.T) {
		os.Unsetenv("TMUX")
		taskWithSession := &storage.Task{
			Meta: storage.TaskMeta{
				ID:       "p-2",
				Sessions: []string{"sess-abc"},
				Links:    map[string]string{},
			},
		}
		m := Model{}
		m.openClaudeMenu(taskWithSession)

		kinds := make([]string, len(m.claudeMenuItems))
		for i, item := range m.claudeMenuItems {
			kinds[i] = item.kind
		}
		expected := []string{"here", "worktree", "resume"}
		if len(kinds) != len(expected) {
			t.Fatalf("items = %v, want %v", kinds, expected)
		}
		for i, k := range expected {
			if kinds[i] != k {
				t.Errorf("item[%d] = %q, want %q", i, kinds[i], k)
			}
		}
	})

	t.Run("resets state on open", func(t *testing.T) {
		m := Model{
			claudeMenuSkipPerms: true,
			claudeMenuCursor:    5,
			claudeMenuItems:     []claudeMenuItem{{"old", "old", "o"}},
		}
		os.Unsetenv("TMUX")
		m.openClaudeMenu(task)

		if m.claudeMenuSkipPerms {
			t.Error("skipPerms should reset to false")
		}
		if m.claudeMenuCursor != 0 {
			t.Errorf("cursor should reset to 0, got %d", m.claudeMenuCursor)
		}
		if m.claudeMenuItems[0].kind == "old" {
			t.Error("items should be rebuilt")
		}
	})
}

func TestUpdateClaudeMenuSkipPerms(t *testing.T) {
	t.Run("toggle with !", func(t *testing.T) {
		m := Model{
			claudeMenu:      true,
			claudeMenuItems: []claudeMenuItem{{"Here", "here", "h"}},
		}

		// First toggle: off -> on
		result, _ := m.updateClaudeMenu(keyMsg("!"))
		m = result.(Model)
		if !m.claudeMenuSkipPerms {
			t.Error("skipPerms should be true after first toggle")
		}

		// Second toggle: on -> off
		result, _ = m.updateClaudeMenu(keyMsg("!"))
		m = result.(Model)
		if m.claudeMenuSkipPerms {
			t.Error("skipPerms should be false after second toggle")
		}
	})

	t.Run("escape closes menu", func(t *testing.T) {
		m := Model{
			claudeMenu:          true,
			claudeMenuSkipPerms: true,
			claudeMenuItems:     []claudeMenuItem{{"Here", "here", "h"}},
		}

		result, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyEsc})
		m = result.(Model)
		if m.claudeMenu {
			t.Error("menu should close on escape")
		}
	})

	t.Run("navigation with j/k", func(t *testing.T) {
		m := Model{
			claudeMenu: true,
			claudeMenuItems: []claudeMenuItem{
				{"Here", "here", "h"},
				{"Worktree", "worktree", "w"},
			},
			claudeMenuCursor: 0,
		}

		// Down
		result, _ := m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyDown})
		m = result.(Model)
		if m.claudeMenuCursor != 1 {
			t.Errorf("cursor = %d, want 1 after down", m.claudeMenuCursor)
		}

		// Down at bottom - stays
		result, _ = m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyDown})
		m = result.(Model)
		if m.claudeMenuCursor != 1 {
			t.Errorf("cursor = %d, want 1 (clamped at bottom)", m.claudeMenuCursor)
		}

		// Up
		result, _ = m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyUp})
		m = result.(Model)
		if m.claudeMenuCursor != 0 {
			t.Errorf("cursor = %d, want 0 after up", m.claudeMenuCursor)
		}

		// Up at top - stays
		result, _ = m.updateClaudeMenu(tea.KeyMsg{Type: tea.KeyUp})
		m = result.(Model)
		if m.claudeMenuCursor != 0 {
			t.Errorf("cursor = %d, want 0 (clamped at top)", m.claudeMenuCursor)
		}
	})
}

func TestViewClaudeMenuSkipPerms(t *testing.T) {
	t.Run("shows unchecked by default", func(t *testing.T) {
		m := Model{
			claudeMenu:          true,
			claudeMenuSkipPerms: false,
			claudeMenuItems:     []claudeMenuItem{{"Here", "here", "h"}},
			width:               80,
			height:              24,
		}
		output := m.viewClaudeMenu()
		if !strings.Contains(output, "[ ]") {
			t.Error("should show unchecked [ ] when skipPerms is false")
		}
		if strings.Contains(output, "[x]") {
			t.Error("should not show [x] when skipPerms is false")
		}
		if !strings.Contains(output, "skip permissions") {
			t.Error("should contain 'skip permissions' label")
		}
	})

	t.Run("shows checked when enabled", func(t *testing.T) {
		m := Model{
			claudeMenu:          true,
			claudeMenuSkipPerms: true,
			claudeMenuItems:     []claudeMenuItem{{"Here", "here", "h"}},
			width:               80,
			height:              24,
		}
		output := m.viewClaudeMenu()
		if !strings.Contains(output, "[x]") {
			t.Error("should show [x] when skipPerms is true")
		}
	})

	t.Run("help text mentions toggle", func(t *testing.T) {
		m := Model{
			claudeMenu:      true,
			claudeMenuItems: []claudeMenuItem{{"Here", "here", "h"}},
			width:           80,
			height:          24,
		}
		output := m.viewClaudeMenu()
		if !strings.Contains(output, "! toggle perms") {
			t.Error("help text should mention ! toggle perms")
		}
	})
}
