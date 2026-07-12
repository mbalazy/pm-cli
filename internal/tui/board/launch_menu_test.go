package board

import (
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// TestMenuTask guards the launch-target resolution: in the detail view the
// launch overlay must act on detailTask (correct even after a [p] parent jump),
// not the board cursor. Regression for launching CC on a child when the user
// jumped to the parent detail and pressed [c].
func TestMenuTask(t *testing.T) {
	parent := &storage.Task{Meta: storage.TaskMeta{ID: "atlas-64", Title: "Parent", Status: storage.StatusDoing}}
	child := &storage.Task{Meta: storage.TaskMeta{ID: "atlas-64-2", Title: "Child", Status: storage.StatusDoing, Parent: "atlas-64"}}

	base := func() Model {
		return Model{
			tasks:    []*storage.Task{parent, child},
			statuses: []storage.TaskStatus{storage.StatusDoing},
			cursors:  []int{0}, // board cursor on first card
		}
	}

	t.Run("detail view returns detailTask, not board cursor", func(t *testing.T) {
		m := base()
		m.currentView = viewDetail
		m.detailTask = parent // jumped to parent via [p]
		if got := m.menuTask(); got != parent {
			t.Fatalf("menuTask() = %v, want parent atlas-64", got.Meta.ID)
		}
	})

	t.Run("board view falls back to selected task", func(t *testing.T) {
		m := base()
		m.currentView = viewBoard
		m.detailTask = parent // stale; must be ignored outside detail view
		if got := m.menuTask(); got != m.selectedTask() {
			t.Fatalf("menuTask() = %v, want board-selected card %v", got, m.selectedTask())
		}
	})

	t.Run("detail view with nil detailTask falls back", func(t *testing.T) {
		m := base()
		m.currentView = viewDetail
		m.detailTask = nil
		if got := m.menuTask(); got == nil {
			t.Fatal("menuTask() = nil, want fallback to board cursor")
		}
	})
}
