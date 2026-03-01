package board

import (
	"testing"

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
