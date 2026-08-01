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

// loadFocusPlan reloads the focus plan from disk. It reports whether the
// load succeeded - callers that follow up with a mutation + saveFocusPlan
// (reload's cleanup pass, handleStalePlan's carry-over) MUST skip that save
// on failure: m.focusPlan is left at its last-known-good value below, and
// saving it back would silently overwrite a corrupt focus.yaml with stale
// data instead of surfacing the read error.
func (m *Model) loadFocusPlan() bool {
	fp, err := storage.ReadFocusPlan(m.store.RootDir())
	if err != nil {
		m.showErrorToast("focus plan", err)
		return false
	}
	m.focusPlan = fp
	m.rebuildFocusSet()
	return true
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
