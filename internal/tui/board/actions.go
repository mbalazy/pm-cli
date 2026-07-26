package board

import (
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// doMoveForward cycles task to next status column.
func (m *Model) doMoveForward(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	idx := m.statusIndex(t.Meta.Status)
	if err := m.store.MoveTask(t, m.statuses[(idx+1)%len(m.statuses)]); err != nil {
		m.showErrorToast("move failed", err)
	}
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
	if err := m.store.MoveTask(t, newStatus); err != nil {
		m.showErrorToast("move failed", err)
	}
	m.reload()
}

// doDone marks task with the last status (typically "done").
func (m *Model) doDone(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "done", task: snapshotTask(t)}
	if err := m.store.MoveTask(t, m.statuses[len(m.statuses)-1]); err != nil {
		m.showErrorToast("move failed", err)
	}
	m.reload()
}

// doWaiting marks task as waiting.
func (m *Model) doWaiting(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "move", task: snapshotTask(t)}
	if err := m.store.MoveTask(t, storage.StatusWaiting); err != nil {
		m.showErrorToast("move failed", err)
	}
	m.reload()
}

// doArchive archives the task.
func (m *Model) doArchive(t *storage.Task) {
	m.lastUndo = &undoAction{kind: "archive", task: snapshotTask(t)}
	if err := m.store.MoveTask(t, storage.StatusArchived); err != nil {
		m.showErrorToast("archive failed", err)
	}
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
	if err := m.store.WriteTask(u.task); err != nil {
		m.showErrorToast("undo failed", err)
		return
	}
	m.lastUndo = nil
	m.toastMsg = "undone: " + u.kind
	m.toastExpiry = time.Now().Add(2 * time.Second)
	m.reload()
}

// showErrorToast surfaces a failed mutation to the user instead of letting it
// fail silently (the card would otherwise just revert with no explanation).
// 15s matches the other real-error toasts in the board (e.g. launch_claude.go,
// launch_executor.go), vs. the 2-5s used for benign info toasts.
func (m *Model) showErrorToast(prefix string, err error) {
	m.toastMsg = prefix + ": " + err.Error()
	m.toastExpiry = time.Now().Add(15 * time.Second)
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

func snapshotTask(t *storage.Task) *storage.Task {
	cp := *t
	cp.Meta = t.Meta
	if t.Meta.Links != nil {
		cp.Meta.Links = make(map[string]string)
		for k, v := range t.Meta.Links {
			cp.Meta.Links[k] = v
		}
	}
	if t.Meta.Tags != nil {
		cp.Meta.Tags = make([]string, len(t.Meta.Tags))
		copy(cp.Meta.Tags, t.Meta.Tags)
	}
	return &cp
}
