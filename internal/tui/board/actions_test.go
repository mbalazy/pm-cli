package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// writeRawTask writes a task file directly to disk, bypassing writeTask's
// validation - simulating a hand-edited frontmatter field (e.g. an invalid
// epic_mode) that only surfaces as an error once the board tries to move it.
func writeRawTask(t *testing.T, m *Model, id, status, epicMode string) {
	t.Helper()
	dir := m.store.ProjectDir("p")
	path := filepath.Join(dir, id+"-a.md")
	content := fmt.Sprintf("---\nid: %s\ntitle: A\nstatus: %s\ncreated: \"2026-07-25\"\nupdated: \"2026-07-25\"\nepic_mode: %s\n---\n", id, status, epicMode)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

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
		store:         store,
		projects:      []string{"all", "p"},
		activeProject: 1,
		menuState:     menuState{hiddenStatuses: make(map[storage.TaskStatus]bool)},
		width:         80,
		height:        24,
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

	t.Run("failed write shows a toast instead of silently reverting", func(t *testing.T) {
		m := newBoardModel(t)
		writeRawTask(t, m, "p-1", "todo", "bogus")
		m.reload()
		m.doMoveForward(m.taskByID("p-1"))
		if !strings.Contains(m.toastMsg, "move failed") || !strings.Contains(m.toastMsg, "epic_mode") {
			t.Errorf("toastMsg = %q, want it to mention the move failure and the reason", m.toastMsg)
		}
		if got := diskStatus(t, m, "p-1"); got != storage.StatusTodo {
			t.Errorf("status = %q, want todo (unchanged - write failed)", got)
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

	t.Run("failed write shows a toast instead of silently reverting", func(t *testing.T) {
		m := newBoardModel(t)
		writeRawTask(t, m, "p-1", "doing", "bogus")
		m.reload()
		m.doMoveBack(m.taskByID("p-1"))
		if !strings.Contains(m.toastMsg, "move failed") || !strings.Contains(m.toastMsg, "epic_mode") {
			t.Errorf("toastMsg = %q, want it to mention the move failure and the reason", m.toastMsg)
		}
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDoing {
			t.Errorf("status = %q, want doing (unchanged - write failed)", got)
		}
	})
}

func TestDoDone(t *testing.T) {
	t.Run("marks task with the last status", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		m.doDone(m.taskByID("p-1"))
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDone {
			t.Errorf("status = %q, want done (last column)", got)
		}
		if m.lastUndo == nil || m.lastUndo.kind != "done" {
			t.Errorf("lastUndo kind = %+v, want done", m.lastUndo)
		}
	})

	t.Run("failed write shows a toast instead of silently reverting", func(t *testing.T) {
		m := newBoardModel(t)
		writeRawTask(t, m, "p-1", "todo", "bogus")
		m.reload()
		m.doDone(m.taskByID("p-1"))
		if !strings.Contains(m.toastMsg, "move failed") {
			t.Errorf("toastMsg = %q, want it to mention the move failure", m.toastMsg)
		}
		if got := diskStatus(t, m, "p-1"); got != storage.StatusTodo {
			t.Errorf("status = %q, want todo (unchanged - write failed)", got)
		}
	})

	// Regression for pm-cli-53: applyColumnVisibility overwrites m.statuses to
	// hold only the VISIBLE columns, so a naive m.statuses[len-1] silently wrote
	// the last visible status instead of the project's real done status.
	t.Run("hidden done column still writes the project's real done status", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		m.hiddenStatuses[storage.StatusDone] = true
		m.reload()
		if got := m.statuses[len(m.statuses)-1]; got == storage.StatusDone {
			t.Fatalf("precondition: done column should be hidden from m.statuses, got last visible = %q", got)
		}
		m.doDone(m.taskByID("p-1"))
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDone {
			t.Errorf("status = %q, want done even with the done column hidden", got)
		}
	})
}

func TestDoWaiting(t *testing.T) {
	t.Run("marks task as waiting", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDoing}})
		m.doWaiting(m.taskByID("p-1"))
		if got := diskStatus(t, m, "p-1"); got != storage.StatusWaiting {
			t.Errorf("status = %q, want waiting", got)
		}
	})

	t.Run("failed write shows a toast instead of silently reverting", func(t *testing.T) {
		m := newBoardModel(t)
		writeRawTask(t, m, "p-1", "doing", "bogus")
		m.reload()
		m.doWaiting(m.taskByID("p-1"))
		if !strings.Contains(m.toastMsg, "move failed") {
			t.Errorf("toastMsg = %q, want it to mention the move failure", m.toastMsg)
		}
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDoing {
			t.Errorf("status = %q, want doing (unchanged - write failed)", got)
		}
	})
}

func TestDoArchive(t *testing.T) {
	t.Run("archives the task", func(t *testing.T) {
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
	})

	t.Run("failed write shows a toast instead of silently reverting", func(t *testing.T) {
		m := newBoardModel(t)
		writeRawTask(t, m, "p-1", "done", "bogus")
		m.reload()
		m.doArchive(m.taskByID("p-1"))
		if !strings.Contains(m.toastMsg, "archive failed") {
			t.Errorf("toastMsg = %q, want it to mention the archive failure", m.toastMsg)
		}
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDone {
			t.Errorf("status = %q, want done (unchanged - write failed)", got)
		}
	})
}

// TestBulkStatusMove covers pm-cli-45: the bulk (select-mode) actions used to
// discard MoveTask's error and still report full success.
func TestBulkStatusMove(t *testing.T) {
	forward := func(m *Model) func(*storage.Task) storage.TaskStatus {
		return func(t *storage.Task) storage.TaskStatus {
			return m.statuses[(m.statusIndex(t.Meta.Status)+1)%len(m.statuses)]
		}
	}

	t.Run("moves every marked task and reports the count", func(t *testing.T) {
		m := newBoardModel(t,
			&storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}},
			&storage.Task{Meta: storage.TaskMeta{ID: "p-2", Title: "B", Status: storage.StatusTodo}},
		)
		m.selecting = true
		m.selected = map[string]bool{"p-1": true, "p-2": true}
		m.bulkStatusMove(m.markedTasks(), "Moved", "forward", forward(m))

		for _, id := range []string{"p-1", "p-2"} {
			if got := diskStatus(t, m, id); got != storage.StatusDoing {
				t.Errorf("%s status = %q, want doing", id, got)
			}
		}
		if m.toastMsg != "Moved 2 tasks forward" {
			t.Errorf("toastMsg = %q, want the plain success message", m.toastMsg)
		}
		if m.selecting || len(m.selected) != 0 {
			t.Errorf("select mode should be cleared, got selecting=%v selected=%v", m.selecting, m.selected)
		}
	})

	t.Run("partial failure moves the rest and reports the split with a reason", func(t *testing.T) {
		m := newBoardModel(t,
			&storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}},
			&storage.Task{Meta: storage.TaskMeta{ID: "p-2", Title: "B", Status: storage.StatusTodo}},
		)
		writeRawTask(t, m, "p-3", "todo", "bogus")
		m.reload()
		m.selecting = true
		m.selected = map[string]bool{"p-1": true, "p-2": true, "p-3": true}
		m.bulkStatusMove(m.markedTasks(), "Moved", "forward", forward(m))

		// One bad task must not strand the good ones.
		for _, id := range []string{"p-1", "p-2"} {
			if got := diskStatus(t, m, id); got != storage.StatusDoing {
				t.Errorf("%s status = %q, want doing (a sibling failure must not block it)", id, got)
			}
		}
		if got := diskStatus(t, m, "p-3"); got != storage.StatusTodo {
			t.Errorf("p-3 status = %q, want todo (unchanged - write failed)", got)
		}
		if !strings.Contains(m.toastMsg, "Moved 2 of 3 tasks forward") ||
			!strings.Contains(m.toastMsg, "1 failed") ||
			!strings.Contains(m.toastMsg, "epic_mode") {
			t.Errorf("toastMsg = %q, want the split, the failure count and the real reason", m.toastMsg)
		}
		// Error timing, not the 2s success timing.
		if time.Until(m.toastExpiry) < 10*time.Second {
			t.Errorf("failure toast expires in %v, want the ~15s error timing", time.Until(m.toastExpiry))
		}
	})

	t.Run("archive reads without a suffix", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDone}})
		m.selected = map[string]bool{"p-1": true}
		m.bulkStatusMove(m.markedTasks(), "Archived", "", func(*storage.Task) storage.TaskStatus {
			return storage.StatusArchived
		})
		if m.toastMsg != "Archived 1 tasks" {
			t.Errorf("toastMsg = %q, want no stray spacing from the empty suffix", m.toastMsg)
		}
	})

	t.Run("no marked tasks is a no-op", func(t *testing.T) {
		m := newBoardModel(t)
		m.toastMsg = "untouched"
		m.bulkStatusMove(nil, "Moved", "forward", forward(m))
		if m.toastMsg != "untouched" {
			t.Errorf("toastMsg = %q, want no toast for an empty selection", m.toastMsg)
		}
	})
}

// TestArchiveRestoreSurfacesErrors covers the other pm-cli-45 call site: the
// archive view's `r` (unarchive), which discarded its MoveTask error too.
func TestArchiveRestoreSurfacesErrors(t *testing.T) {
	m := newBoardModel(t)
	writeRawTask(t, m, "p-1", "archived", "bogus")
	m.reload()
	m.currentView = viewArchive

	result, _ := m.updateArchive(keyMsg("r"))
	got, ok := result.(Model)
	if !ok {
		t.Fatalf("updateArchive returned %T, want Model", result)
	}
	if !strings.Contains(got.toastMsg, "restore failed") || !strings.Contains(got.toastMsg, "epic_mode") {
		t.Errorf("toastMsg = %q, want it to mention the restore failure and the reason", got.toastMsg)
	}
	if s := diskStatus(t, m, "p-1"); s != storage.StatusArchived {
		t.Errorf("status = %q, want archived (unchanged - write failed)", s)
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

	t.Run("failed write shows a toast and keeps lastUndo for a retry", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		bad := snapshotTask(m.taskByID("p-1"))
		bad.Meta.EpicMode = "bogus"
		m.lastUndo = &undoAction{kind: "move", task: bad}

		m.doUndo()

		if !strings.Contains(m.toastMsg, "undo failed") || !strings.Contains(m.toastMsg, "epic_mode") {
			t.Errorf("toastMsg = %q, want it to mention the undo failure and the reason", m.toastMsg)
		}
		if m.lastUndo == nil {
			t.Error("lastUndo should be preserved after a failed undo so the user can retry")
		}
		if got := diskStatus(t, m, "p-1"); got != storage.StatusTodo {
			t.Errorf("status = %q, want todo (unchanged - write failed)", got)
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
