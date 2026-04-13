package board

import (
	"os"
	"os/exec"
	"path/filepath"
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

	t.Run("sorted by id number asc", func(t *testing.T) {
		todos := m.filteredTasks(storage.StatusTodo)
		if len(todos) < 2 {
			t.Fatal("need at least 2 todos")
		}
		if todos[0].Meta.ID != "a-1" || todos[1].Meta.ID != "a-5" {
			t.Errorf("got [%s, %s], want [a-1, a-5]", todos[0].Meta.ID, todos[1].Meta.ID)
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

func TestMatchesQuery(t *testing.T) {
	task := &storage.Task{
		Meta: storage.TaskMeta{
			ID:       "proj-42",
			Title:    "Implement Auth Flow",
			Brief:    "Working on OAuth integration",
			Branch:   "feat/auth-flow",
			Tags:     []string{"backend", "security"},
			Links:    map[string]string{"jira": "https://jira.example.com/PROJ-42", "figma": "https://figma.com/design"},
			Sessions: []string{"abc-123-def"},
		},
		Body: "Need to add JWT token validation to the middleware",
	}

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"matches title", "auth flow", true},
		{"matches title case insensitive", "AUTH FLOW", true},
		{"matches ID", "proj-42", true},
		{"matches body", "jwt token", true},
		{"matches brief", "oauth", true},
		{"matches branch", "feat/auth", true},
		{"matches tag", "security", true},
		{"matches link key", "figma", true},
		{"matches link value", "jira.example", true},
		{"matches session", "abc-123", true},
		{"no match", "nonexistent", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesQuery(task, strings.ToLower(tt.query))
			if got != tt.want {
				t.Errorf("matchesQuery(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
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
		// Without tmux: here, worktree, project
		expected := []string{"here", "worktree", "project"}
		if len(kinds) != len(expected) {
			t.Fatalf("items = %v, want %v", kinds, expected)
		}
		for i, k := range expected {
			if kinds[i] != k {
				t.Errorf("item[%d] = %q, want %q", i, kinds[i], k)
			}
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
		// With tmux: here, tmux, worktree, worktree-tmux, project
		expected := []string{"here", "tmux", "worktree", "worktree-tmux", "project"}
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
		expected := []string{"here", "resume", "worktree", "project"}
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

func TestTmuxWindowName(t *testing.T) {
	tests := []struct {
		prefix, taskID, sessionID, want string
	}{
		{"cc", "protem-12", "a1b2c3d4-xxxx-yyyy", "cc:a1b2:protem-12"},
		{"wt", "proj-5", "deadbeef-1234", "wt:dead:proj-5"},
		{"cc", "t-1", "ab", "cc:ab:t-1"},   // short session
		{"cc", "t-1", "abcd", "cc:abcd:t-1"}, // exactly 4
	}
	for _, tt := range tests {
		got := tmuxWindowName(tt.prefix, tt.taskID, tt.sessionID)
		if got != tt.want {
			t.Errorf("tmuxWindowName(%q, %q, %q) = %q, want %q", tt.prefix, tt.taskID, tt.sessionID, got, tt.want)
		}
	}
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

func TestVisibleProjects(t *testing.T) {
	t.Run("no hidden returns all", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta", "gamma"},
			hiddenProjects: make(map[string]bool),
		}
		got := m.visibleProjects()
		if len(got) != 4 {
			t.Errorf("got %d visible, want 4", len(got))
		}
	})

	t.Run("hidden project excluded", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta", "gamma"},
			hiddenProjects: map[string]bool{"beta": true},
		}
		got := m.visibleProjects()
		if len(got) != 3 {
			t.Errorf("got %d visible, want 3", len(got))
		}
		for _, p := range got {
			if p == "beta" {
				t.Error("beta should be hidden")
			}
		}
	})

	t.Run("all never hidden", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha"},
			hiddenProjects: map[string]bool{"all": true, "alpha": true},
		}
		got := m.visibleProjects()
		if len(got) != 1 || got[0] != "all" {
			t.Errorf("got %v, want [all]", got)
		}
	})
}

func TestNextVisibleProject(t *testing.T) {
	t.Run("forward skips hidden", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta", "gamma"},
			activeProject:  1, // alpha
			hiddenProjects: map[string]bool{"beta": true},
		}
		got := m.nextVisibleProject(1)
		if m.projects[got] != "gamma" {
			t.Errorf("got %q, want gamma", m.projects[got])
		}
	})

	t.Run("backward skips hidden", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta", "gamma"},
			activeProject:  3, // gamma
			hiddenProjects: map[string]bool{"beta": true},
		}
		got := m.nextVisibleProject(-1)
		if m.projects[got] != "alpha" {
			t.Errorf("got %q, want alpha", m.projects[got])
		}
	})

	t.Run("wraps around", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta"},
			activeProject:  2, // beta
			hiddenProjects: make(map[string]bool),
		}
		got := m.nextVisibleProject(1)
		if m.projects[got] != "all" {
			t.Errorf("got %q, want all (wrap)", m.projects[got])
		}
	})

	t.Run("all hidden wraps to all", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta"},
			activeProject:  0,
			hiddenProjects: map[string]bool{"alpha": true, "beta": true},
		}
		got := m.nextVisibleProject(1)
		if m.projects[got] != "all" {
			t.Errorf("got %q, want all", m.projects[got])
		}
	})
}

func TestFilteredPickerItems(t *testing.T) {
	items := []pickerItem{
		{slug: "atlas", name: "Atlas", stack: "Elixir", taskCount: 10},
		{slug: "acme-api", name: "ACME-API", stack: "Livingdocs", taskCount: 5},
		{slug: "pm-cli", name: "pm-cli", stack: "Go, Bubble Tea", taskCount: 8},
	}

	t.Run("no filter returns all", func(t *testing.T) {
		m := Model{pickerItems: items, pickerFilter: ""}
		got := m.filteredPickerItems()
		if len(got) != 3 {
			t.Errorf("got %d items, want 3", len(got))
		}
	})

	t.Run("filters by name", func(t *testing.T) {
		m := Model{pickerItems: items, pickerFilter: "atlas"}
		got := m.filteredPickerItems()
		if len(got) != 1 || got[0].slug != "atlas" {
			t.Errorf("got %v, want [atlas]", got)
		}
	})

	t.Run("filters by stack", func(t *testing.T) {
		m := Model{pickerItems: items, pickerFilter: "elixir"}
		got := m.filteredPickerItems()
		if len(got) != 1 || got[0].slug != "atlas" {
			t.Errorf("got %v, want [atlas]", got)
		}
	})

	t.Run("filters by slug", func(t *testing.T) {
		m := Model{pickerItems: items, pickerFilter: "pm-cli"}
		got := m.filteredPickerItems()
		if len(got) != 1 || got[0].slug != "pm-cli" {
			t.Errorf("got %v, want [pm-cli]", got)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		m := Model{pickerItems: items, pickerFilter: "ACME-API"}
		got := m.filteredPickerItems()
		if len(got) != 1 || got[0].slug != "acme-api" {
			t.Errorf("got %v, want [acme-api]", got)
		}
	})
}

func TestSortPickerItems(t *testing.T) {
	t.Run("hidden projects last", func(t *testing.T) {
		m := Model{
			pickerItems: []pickerItem{
				{slug: "hidden", hidden: true, taskCount: 100},
				{slug: "visible", hidden: false, taskCount: 1},
			},
		}
		m.sortPickerItems()
		if m.pickerItems[0].slug != "visible" {
			t.Errorf("first item should be visible, got %q", m.pickerItems[0].slug)
		}
	})

	t.Run("preserves insertion order among visible", func(t *testing.T) {
		m := Model{
			pickerItems: []pickerItem{
				{slug: "alpha", doingCount: 0, taskCount: 1},
				{slug: "beta", doingCount: 5, taskCount: 50},
				{slug: "gamma", doingCount: 0, taskCount: 10},
			},
		}
		m.sortPickerItems()
		// should keep original order, not sort by doing/count
		if m.pickerItems[0].slug != "alpha" || m.pickerItems[1].slug != "beta" || m.pickerItems[2].slug != "gamma" {
			t.Errorf("should preserve order, got [%s, %s, %s]", m.pickerItems[0].slug, m.pickerItems[1].slug, m.pickerItems[2].slug)
		}
	})
}

func TestApplyProjectOrder(t *testing.T) {
	t.Run("no order returns original", func(t *testing.T) {
		projects := []string{"beta", "alpha", "gamma"}
		got := applyProjectOrder(projects, nil)
		if got[0] != "beta" || got[1] != "alpha" || got[2] != "gamma" {
			t.Errorf("got %v, want [beta alpha gamma]", got)
		}
	})

	t.Run("reorders according to saved order", func(t *testing.T) {
		projects := []string{"beta", "alpha", "gamma"}
		order := []string{"gamma", "alpha", "beta"}
		got := applyProjectOrder(projects, order)
		if got[0] != "gamma" || got[1] != "alpha" || got[2] != "beta" {
			t.Errorf("got %v, want [gamma alpha beta]", got)
		}
	})

	t.Run("new projects go to end", func(t *testing.T) {
		projects := []string{"beta", "alpha", "new-proj"}
		order := []string{"alpha", "beta"}
		got := applyProjectOrder(projects, order)
		if got[0] != "alpha" || got[1] != "beta" || got[2] != "new-proj" {
			t.Errorf("got %v, want [alpha beta new-proj]", got)
		}
	})

	t.Run("removed projects in order are ignored", func(t *testing.T) {
		projects := []string{"alpha", "gamma"}
		order := []string{"gamma", "removed", "alpha"}
		got := applyProjectOrder(projects, order)
		if got[0] != "gamma" || got[1] != "alpha" {
			t.Errorf("got %v, want [gamma alpha]", got)
		}
	})
}

func TestPickerReorder(t *testing.T) {
	t.Run("move down", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta", "gamma"},
			activeProject:  1,
			hiddenProjects: make(map[string]bool),
			pickerItems: []pickerItem{
				{slug: "alpha"}, {slug: "beta"}, {slug: "gamma"},
			},
			pickerCursor: 0,
		}
		m.pickerReorder(1)
		if m.pickerItems[0].slug != "beta" || m.pickerItems[1].slug != "alpha" {
			t.Errorf("got [%s, %s], want [beta, alpha]", m.pickerItems[0].slug, m.pickerItems[1].slug)
		}
		if m.pickerCursor != 1 {
			t.Errorf("cursor should follow item, got %d", m.pickerCursor)
		}
		// m.projects should reflect new order
		if m.projects[1] != "beta" || m.projects[2] != "alpha" {
			t.Errorf("projects = %v, want [all beta alpha gamma]", m.projects)
		}
	})

	t.Run("move up", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta", "gamma"},
			activeProject:  2,
			hiddenProjects: make(map[string]bool),
			pickerItems: []pickerItem{
				{slug: "alpha"}, {slug: "beta"}, {slug: "gamma"},
			},
			pickerCursor: 2,
		}
		m.pickerReorder(-1)
		if m.pickerItems[1].slug != "gamma" || m.pickerItems[2].slug != "beta" {
			t.Errorf("got [%s, %s, %s]", m.pickerItems[0].slug, m.pickerItems[1].slug, m.pickerItems[2].slug)
		}
		if m.pickerCursor != 1 {
			t.Errorf("cursor = %d, want 1", m.pickerCursor)
		}
	})

	t.Run("no swap across hidden boundary", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta"},
			hiddenProjects: make(map[string]bool),
			pickerItems: []pickerItem{
				{slug: "alpha", hidden: false},
				{slug: "beta", hidden: true},
			},
			pickerCursor: 0,
		}
		m.pickerReorder(1)
		// should not swap
		if m.pickerItems[0].slug != "alpha" {
			t.Errorf("should not swap across hidden boundary, got %s first", m.pickerItems[0].slug)
		}
	})

	t.Run("preserves active project", func(t *testing.T) {
		m := Model{
			projects:       []string{"all", "alpha", "beta", "gamma"},
			activeProject:  3, // gamma
			hiddenProjects: make(map[string]bool),
			pickerItems: []pickerItem{
				{slug: "alpha"}, {slug: "beta"}, {slug: "gamma"},
			},
			pickerCursor: 0,
		}
		m.pickerReorder(1)
		// gamma was at index 3, after swap alpha<->beta it should still be at index 3
		if m.projects[m.activeProject] != "gamma" {
			t.Errorf("active project should still be gamma, got %q at index %d", m.projects[m.activeProject], m.activeProject)
		}
	})
}

func TestMarkedTasks(t *testing.T) {
	tasks := makeTasks()
	m := Model{
		tasks:    tasks,
		selected: make(map[string]bool),
	}

	t.Run("empty selection", func(t *testing.T) {
		got := m.markedTasks()
		if len(got) != 0 {
			t.Errorf("expected 0 marked tasks, got %d", len(got))
		}
	})

	t.Run("select two tasks", func(t *testing.T) {
		m.selected["a-1"] = true
		m.selected["a-3"] = true
		got := m.markedTasks()
		if len(got) != 2 {
			t.Errorf("expected 2 marked tasks, got %d", len(got))
		}
		ids := map[string]bool{}
		for _, t := range got {
			ids[t.Meta.ID] = true
		}
		if !ids["a-1"] || !ids["a-3"] {
			t.Errorf("expected a-1 and a-3, got %v", ids)
		}
	})

	t.Run("deselect clears", func(t *testing.T) {
		delete(m.selected, "a-1")
		delete(m.selected, "a-3")
		got := m.markedTasks()
		if len(got) != 0 {
			t.Errorf("expected 0 after deselect, got %d", len(got))
		}
	})
}

func TestSelectModeToggle(t *testing.T) {
	m := Model{
		tasks:    makeTasks(),
		statuses: []storage.TaskStatus{storage.StatusTodo, storage.StatusDoing, storage.StatusDone},
		cursors:  []int{0, 0, 0},
		scrollOffsets: []int{0, 0, 0},
		selected: make(map[string]bool),
		width:    80,
		height:   24,
	}

	// Enter select mode
	m.selecting = true
	if t2 := m.selectedTask(); t2 != nil {
		m.selected[t2.Meta.ID] = true
	}

	if !m.selecting {
		t.Error("expected selecting = true")
	}
	if len(m.selected) != 1 {
		t.Errorf("expected 1 selected, got %d", len(m.selected))
	}
	if !m.selected["a-1"] {
		t.Errorf("expected a-1 selected, got %v", m.selected)
	}

	// Toggle off
	delete(m.selected, "a-1")
	if len(m.selected) != 0 {
		t.Error("expected 0 after toggle off")
	}

	// Exit select mode
	m.selecting = false
	m.selected = make(map[string]bool)
	if m.selecting {
		t.Error("expected selecting = false after exit")
	}
}

func TestCopyWorktreeFiles(t *testing.T) {
	// Set up a git repo in a temp dir
	projDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = projDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s", args, out)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "test@test.com")
	run("git", "config", "user.name", "Test")
	os.WriteFile(filepath.Join(projDir, "main.go"), []byte("package main"), 0644)
	run("git", "add", ".")
	run("git", "commit", "-m", "init")

	// Create untracked files (not in git)
	os.WriteFile(filepath.Join(projDir, ".env"), []byte("SECRET=abc"), 0644)
	os.WriteFile(filepath.Join(projDir, ".env.local"), []byte("LOCAL=xyz"), 0600)
	os.MkdirAll(filepath.Join(projDir, "config"), 0755)
	os.WriteFile(filepath.Join(projDir, "config", "local.json"), []byte(`{"db":"localhost"}`), 0644)

	t.Run("copies all untracked files", func(t *testing.T) {
		wtName := "test-copy"
		copyWorktreeFiles(projDir, wtName)

		wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)

		// Verify worktree was created
		if _, err := os.Stat(wtPath); os.IsNotExist(err) {
			t.Fatal("worktree dir was not created")
		}

		// Verify all untracked files were copied
		for _, f := range []string{".env", ".env.local", "config/local.json"} {
			data, err := os.ReadFile(filepath.Join(wtPath, f))
			if err != nil {
				t.Errorf("file %s not copied: %v", f, err)
				continue
			}
			orig, _ := os.ReadFile(filepath.Join(projDir, f))
			if string(data) != string(orig) {
				t.Errorf("file %s content mismatch: got %q, want %q", f, data, orig)
			}
		}

		// Verify file permissions preserved
		info, _ := os.Stat(filepath.Join(wtPath, ".env.local"))
		if info.Mode().Perm() != 0600 {
			t.Errorf(".env.local perms = %o, want 0600", info.Mode().Perm())
		}
	})

	t.Run("skips existing files", func(t *testing.T) {
		wtName := "test-skip"
		wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)

		// Create worktree with files
		copyWorktreeFiles(projDir, wtName)
		// Modify a file in the worktree
		os.WriteFile(filepath.Join(wtPath, ".env"), []byte("MODIFIED"), 0644)

		// Copy again - should NOT overwrite
		copyWorktreeFiles(projDir, wtName)
		data, _ := os.ReadFile(filepath.Join(wtPath, ".env"))
		if string(data) != "MODIFIED" {
			t.Errorf("existing file was overwritten: got %q, want %q", data, "MODIFIED")
		}
	})

	t.Run("does not copy tracked files", func(t *testing.T) {
		wtName := "test-tracked"
		copyWorktreeFiles(projDir, wtName)

		wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)
		// main.go is tracked - it should come from git, not our copy
		// Verify the tracked file exists (from git worktree add)
		data, err := os.ReadFile(filepath.Join(wtPath, "main.go"))
		if err != nil {
			t.Fatal("tracked file main.go missing from worktree")
		}
		if string(data) != "package main" {
			t.Errorf("tracked file content wrong: %q", data)
		}
	})
}
