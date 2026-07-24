package board

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// newBoardModel builds a Model backed by a real Store on a temp dir with the
// given tasks persisted in project "p". MoveTask self-locks since 0.23.0, so
// tests must never wrap do* calls in LockProject.
func newBoardModel(t *testing.T, tasks ...*storage.Task) *Model {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("p", &storage.Project{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if err := store.AddTask("p", task); err != nil {
			t.Fatal(err)
		}
	}
	m := &Model{
		store:          store,
		projects:       []string{"all", "p"},
		activeProject:  1,
		hiddenStatuses: make(map[storage.TaskStatus]bool),
		width:          80,
		height:         24,
	}
	m.reload()
	return m
}

// diskStatus reads a task's status fresh from disk.
func diskStatus(t *testing.T, m *Model, id string) storage.TaskStatus {
	t.Helper()
	task, err := m.store.FindTask("p", id)
	if err != nil {
		t.Fatal(err)
	}
	return task.Meta.Status
}

func (m *Model) taskByID(id string) *storage.Task {
	for _, t := range m.tasks {
		if t.Meta.ID == id {
			return t
		}
	}
	return nil
}

func TestDoMoveForward(t *testing.T) {
	t.Run("advances to the next status column", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		m.doMoveForward(m.taskByID("p-1"))
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDoing {
			t.Errorf("status = %q, want doing", got)
		}
		if m.lastUndo == nil || m.lastUndo.kind != "move" || m.lastUndo.task.Meta.Status != storage.StatusTodo {
			t.Errorf("lastUndo should snapshot the pre-move task, got %+v", m.lastUndo)
		}
	})

	t.Run("wraps from the last column to the first", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDone}})
		m.doMoveForward(m.taskByID("p-1"))
		if got := diskStatus(t, m, "p-1"); got != storage.StatusTodo {
			t.Errorf("status = %q, want todo (wrap)", got)
		}
	})
}

func TestDoMoveBack(t *testing.T) {
	t.Run("moves to the previous status column", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDoing}})
		m.doMoveBack(m.taskByID("p-1"))
		if got := diskStatus(t, m, "p-1"); got != storage.StatusTodo {
			t.Errorf("status = %q, want todo", got)
		}
	})

	t.Run("wraps from the first column to the last", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		m.doMoveBack(m.taskByID("p-1"))
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDone {
			t.Errorf("status = %q, want done (wrap)", got)
		}
	})
}

func TestDoDone(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
	m.doDone(m.taskByID("p-1"))
	if got := diskStatus(t, m, "p-1"); got != storage.StatusDone {
		t.Errorf("status = %q, want done (last column)", got)
	}
	if m.lastUndo == nil || m.lastUndo.kind != "done" {
		t.Errorf("lastUndo kind = %+v, want done", m.lastUndo)
	}
}

func TestDoWaiting(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDoing}})
	m.doWaiting(m.taskByID("p-1"))
	if got := diskStatus(t, m, "p-1"); got != storage.StatusWaiting {
		t.Errorf("status = %q, want waiting", got)
	}
}

func TestDoArchive(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDone}})
	m.doArchive(m.taskByID("p-1"))
	if got := diskStatus(t, m, "p-1"); got != storage.StatusArchived {
		t.Errorf("status = %q, want archived", got)
	}
	if m.lastUndo == nil || m.lastUndo.kind != "archive" {
		t.Errorf("lastUndo kind = %+v, want archive", m.lastUndo)
	}
	// Archived tasks disappear from every board column.
	for i := range m.statuses {
		for _, task := range m.columnTasks(i) {
			if task.Meta.ID == "p-1" {
				t.Errorf("archived task still visible in column %d", i)
			}
		}
	}
}

func TestDoUndo(t *testing.T) {
	t.Run("nothing to undo shows a toast", func(t *testing.T) {
		m := newBoardModel(t)
		m.doUndo()
		if m.toastMsg != "nothing to undo" {
			t.Errorf("toastMsg = %q, want 'nothing to undo'", m.toastMsg)
		}
	})

	t.Run("restores the snapshot on disk and clears lastUndo", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		m.doDone(m.taskByID("p-1"))
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDone {
			t.Fatalf("precondition: status = %q, want done", got)
		}

		m.doUndo()
		if got := diskStatus(t, m, "p-1"); got != storage.StatusTodo {
			t.Errorf("status after undo = %q, want todo", got)
		}
		if m.lastUndo != nil {
			t.Error("lastUndo should be cleared after undo")
		}
		if !strings.Contains(m.toastMsg, "undone: done") {
			t.Errorf("toastMsg = %q, want 'undone: done'", m.toastMsg)
		}
	})
}

func TestDoReorder(t *testing.T) {
	threeTodos := func() []*storage.Task {
		return []*storage.Task{
			{Meta: storage.TaskMeta{ID: "p-1", Title: "First", Status: storage.StatusTodo}},
			{Meta: storage.TaskMeta{ID: "p-2", Title: "Second", Status: storage.StatusTodo}},
			{Meta: storage.TaskMeta{ID: "p-3", Title: "Third", Status: storage.StatusTodo}},
		}
	}

	t.Run("all-zero orders get sequential orders, then the pair swaps", func(t *testing.T) {
		m := newBoardModel(t, threeTodos()...)
		m.activeCol = 0 // todo column; all Order==0 -> sorted by numeric ID: p-1, p-2, p-3
		m.cursors[0] = 0

		m.doReorder(1)

		col := m.columnTasks(0)
		if len(col) != 3 {
			t.Fatalf("column has %d tasks, want 3", len(col))
		}
		if col[0].Meta.ID != "p-2" || col[1].Meta.ID != "p-1" || col[2].Meta.ID != "p-3" {
			t.Errorf("column order = [%s %s %s], want [p-2 p-1 p-3]",
				col[0].Meta.ID, col[1].Meta.ID, col[2].Meta.ID)
		}
		if m.cursors[0] != 1 {
			t.Errorf("cursor = %d, want 1 (follows the moved task)", m.cursors[0])
		}
		// Orders persisted: p-2 took p-1's slot (10) and vice versa.
		moved, _ := m.store.FindTask("p", "p-1")
		swapped, _ := m.store.FindTask("p", "p-2")
		if moved.Meta.Order != 20 || swapped.Meta.Order != 10 {
			t.Errorf("orders = p-1:%d p-2:%d, want p-1:20 p-2:10", moved.Meta.Order, swapped.Meta.Order)
		}
	})

	t.Run("move up swaps with the previous task", func(t *testing.T) {
		m := newBoardModel(t, threeTodos()...)
		m.activeCol = 0
		m.cursors[0] = 2 // p-3

		m.doReorder(-1)

		col := m.columnTasks(0)
		if col[1].Meta.ID != "p-3" || col[2].Meta.ID != "p-2" {
			t.Errorf("column order = [%s %s %s], want [p-1 p-3 p-2]",
				col[0].Meta.ID, col[1].Meta.ID, col[2].Meta.ID)
		}
		if m.cursors[0] != 1 {
			t.Errorf("cursor = %d, want 1", m.cursors[0])
		}
	})

	t.Run("no-op at the column edge", func(t *testing.T) {
		m := newBoardModel(t, threeTodos()...)
		m.activeCol = 0
		m.cursors[0] = 2 // last task, move down has no target

		m.doReorder(1)

		col := m.columnTasks(0)
		if col[0].Meta.ID != "p-1" || col[1].Meta.ID != "p-2" || col[2].Meta.ID != "p-3" {
			t.Errorf("edge reorder must not change order, got [%s %s %s]",
				col[0].Meta.ID, col[1].Meta.ID, col[2].Meta.ID)
		}
		if m.cursors[0] != 2 {
			t.Errorf("cursor = %d, want 2 (unchanged)", m.cursors[0])
		}
	})

	t.Run("no-op with fewer than two tasks", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Only", Status: storage.StatusTodo}})
		m.activeCol = 0
		m.doReorder(1)
		if got, _ := m.store.FindTask("p", "p-1"); got.Meta.Order != 0 {
			t.Errorf("single-task reorder must not assign orders, got %d", got.Meta.Order)
		}
	})
}
