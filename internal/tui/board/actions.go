package board

import (
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// doMoveForward cycles task to next status column.
func (m *Model) doMoveForward(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	idx := m.statusIndex(t.Meta.Status)
	m.store.MoveTask(t, m.statuses[(idx+1)%len(m.statuses)])
	m.reload()
}

// doMoveBack cycles task to previous status column.
func (m *Model) doMoveBack(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	idx := m.statusIndex(t.Meta.Status)
	var newStatus storage.TaskStatus
	if idx > 0 {
		newStatus = m.statuses[idx-1]
	} else {
		newStatus = m.statuses[len(m.statuses)-1]
	}
	m.store.MoveTask(t, newStatus)
	m.reload()
}

// doDone marks task with the last status (typically "done").
func (m *Model) doDone(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "done", task: snapshotTask(t)}
	m.store.MoveTask(t, m.statuses[len(m.statuses)-1])
	m.reload()
}

// doWaiting marks task as waiting.
func (m *Model) doWaiting(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	m.store.MoveTask(t, storage.StatusWaiting)
	m.reload()
}

// doArchive archives the task.
func (m *Model) doArchive(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "archive", task: snapshotTask(t)}
	m.store.MoveTask(t, storage.StatusArchived)
	m.reload()
}

// doUndo restores the last undone action.
func (m *Model) doUndo() {
	if m.lastUndo == nil {
		m.toastMsg = "nothing to undo"
		m.toastExpiry = time.Now().Add(2 * time.Second)
		return
	}
	u := m.lastUndo
	m.store.WriteTask(u.task)
	m.lastUndo = nil
	m.toastMsg = "undone: " + u.kind
	m.toastExpiry = time.Now().Add(2 * time.Second)
	m.reload()
}

// doReorder swaps the selected task with a neighbor in the column.
// direction: +1 = move down, -1 = move up.
func (m *Model) doReorder(direction int) {
	tasks := m.columnTasks(m.activeCol)
	if len(tasks) < 2 {
		return
	}
	cursor := m.cursors[m.activeCol]
	target := cursor + direction
	if target < 0 || target >= len(tasks) {
		return
	}

	a := tasks[cursor]
	b := tasks[target]

	// If all tasks have Order==0, assign sequential orders to all tasks in column
	allZero := true
	for _, t := range tasks {
		if t.Meta.Order != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		for i, t := range tasks {
			t.Meta.Order = (i + 1) * 10
			t.Meta.Updated = storage.Today()
			m.store.WriteTask(t)
		}
	}

	// Swap orders
	a.Meta.Order, b.Meta.Order = b.Meta.Order, a.Meta.Order
	a.Meta.Updated = storage.Today()
	b.Meta.Updated = storage.Today()
	m.store.WriteTask(a)
	m.store.WriteTask(b)

	m.cursors[m.activeCol] = target
	m.reload()
}
