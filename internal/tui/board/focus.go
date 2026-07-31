package board

import (
	"fmt"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func (m *Model) rebuildFocusSet() {
	m.focusSet = make(map[string]bool, len(m.focusPlan.Tasks))
	for _, id := range m.focusPlan.Tasks {
		m.focusSet[id] = true
	}
}

func (m *Model) loadFocusPlan() {
	m.focusPlan = storage.ReadFocusPlan(m.store.RootDir())
	m.rebuildFocusSet()
}

func (m *Model) saveFocusPlan() {
	storage.WriteFocusPlan(m.store.RootDir(), m.focusPlan)
}

func (m *Model) handleStalePlan() {
	if !m.focusPlan.IsStale() {
		return
	}
	allTasks, _ := m.store.GetAllTasks()
	m.focusPlan.Cleanup(allTasks)
	n := len(m.focusPlan.Tasks)
	m.focusPlan.Date = storage.Today()
	m.saveFocusPlan()
	m.rebuildFocusSet()
	if n > 0 {
		m.toastMsg = fmt.Sprintf("Focus carried over from yesterday (%d tasks)", n)
		m.toastExpiry = time.Now().Add(4 * time.Second)
	}
}

func (m *Model) focusTasks() []*storage.Task {
	if m.focusTaskLookup == nil {
		// Only hit before the first reload() (e.g. a Model built without New()).
		allTasks, _ := m.store.GetAllTasks()
		m.focusTaskLookup = make(map[string]*storage.Task, len(allTasks))
		for _, t := range allTasks {
			m.focusTaskLookup[t.Meta.ID] = t
		}
	}
	var result []*storage.Task
	for _, id := range m.focusPlan.Tasks {
		if t, ok := m.focusTaskLookup[id]; ok {
			result = append(result, t)
		}
	}
	return result
}

func (m Model) selectedFocusTask() *storage.Task {
	tasks := m.focusTasks()
	if len(tasks) == 0 {
		return nil
	}
	idx := m.focusCursor
	if idx >= len(tasks) {
		idx = len(tasks) - 1
	}
	return tasks[idx]
}

func (m *Model) fixFocusCursor() {
	tasks := m.focusTasks()
	if m.focusCursor >= len(tasks) && len(tasks) > 0 {
		m.focusCursor = len(tasks) - 1
	}
	if len(tasks) == 0 {
		m.focusCursor = 0
	}
}
