package board

import (
	"fmt"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func focusFixture(t *testing.T) *Model {
	t.Helper()
	return newBoardModel(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}},
		&storage.Task{Meta: storage.TaskMeta{ID: "p-2", Title: "B", Status: storage.StatusDoing}},
		&storage.Task{Meta: storage.TaskMeta{ID: "p-3", Title: "C", Status: storage.StatusDone}},
	)
}

func TestSaveAndLoadFocusPlan(t *testing.T) {
	m := focusFixture(t)
	m.focusPlan = storage.FocusPlan{Date: storage.Today(), Tasks: []string{"p-2", "p-1"}}
	m.saveFocusPlan()

	m.focusPlan = storage.FocusPlan{}
	m.loadFocusPlan()
	if m.focusPlan.Date != storage.Today() {
		t.Errorf("Date = %q, want today", m.focusPlan.Date)
	}
	if len(m.focusPlan.Tasks) != 2 || m.focusPlan.Tasks[0] != "p-2" || m.focusPlan.Tasks[1] != "p-1" {
		t.Errorf("Tasks = %v, want [p-2 p-1] (order preserved)", m.focusPlan.Tasks)
	}
	if !m.focusSet["p-2"] || !m.focusSet["p-1"] || m.focusSet["p-3"] {
		t.Errorf("focusSet not rebuilt from plan: %v", m.focusSet)
	}
}

func TestHandleStalePlan(t *testing.T) {
	t.Run("fresh plan is untouched", func(t *testing.T) {
		m := focusFixture(t)
		m.focusPlan = storage.FocusPlan{Date: storage.Today(), Tasks: []string{"p-1"}}
		m.handleStalePlan()
		if len(m.focusPlan.Tasks) != 1 || m.toastMsg != "" {
			t.Errorf("fresh plan must be a no-op, tasks=%v toast=%q", m.focusPlan.Tasks, m.toastMsg)
		}
	})

	t.Run("empty date is not stale", func(t *testing.T) {
		m := focusFixture(t)
		m.focusPlan = storage.FocusPlan{}
		m.handleStalePlan()
		if m.focusPlan.Date != "" || m.toastMsg != "" {
			t.Errorf("empty plan must stay empty, got date=%q toast=%q", m.focusPlan.Date, m.toastMsg)
		}
	})

	t.Run("stale plan carries over live tasks and drops done/missing", func(t *testing.T) {
		m := focusFixture(t)
		// p-1 live, p-3 done, ghost never existed.
		m.focusPlan = storage.FocusPlan{Date: "2020-01-01", Tasks: []string{"p-1", "p-3", "ghost"}}
		m.handleStalePlan()

		if len(m.focusPlan.Tasks) != 1 || m.focusPlan.Tasks[0] != "p-1" {
			t.Errorf("Tasks = %v, want [p-1]", m.focusPlan.Tasks)
		}
		if m.focusPlan.Date != storage.Today() {
			t.Errorf("Date = %q, want today", m.focusPlan.Date)
		}
		if m.toastMsg != fmt.Sprintf("Focus carried over from yesterday (%d tasks)", 1) {
			t.Errorf("toastMsg = %q", m.toastMsg)
		}
		if !m.focusSet["p-1"] || m.focusSet["p-3"] {
			t.Errorf("focusSet = %v, want only p-1", m.focusSet)
		}
		// Persisted: a fresh read sees the carried-over plan.
		if got := storage.ReadFocusPlan(m.store.RootDir()); got.Date != storage.Today() || len(got.Tasks) != 1 {
			t.Errorf("plan not persisted, got %+v", got)
		}
	})

	t.Run("stale plan with nothing to carry shows no toast", func(t *testing.T) {
		m := focusFixture(t)
		m.focusPlan = storage.FocusPlan{Date: "2020-01-01", Tasks: []string{"p-3", "ghost"}}
		m.handleStalePlan()
		if len(m.focusPlan.Tasks) != 0 {
			t.Errorf("Tasks = %v, want empty", m.focusPlan.Tasks)
		}
		if m.toastMsg != "" {
			t.Errorf("no surviving tasks must not toast, got %q", m.toastMsg)
		}
		if m.focusPlan.Date != storage.Today() {
			t.Errorf("Date = %q, want today (still refreshed)", m.focusPlan.Date)
		}
	})
}

func TestFocusTasks(t *testing.T) {
	m := focusFixture(t)

	t.Run("empty plan yields no tasks", func(t *testing.T) {
		m.focusPlan = storage.FocusPlan{}
		if got := m.focusTasks(); len(got) != 0 {
			t.Errorf("got %d tasks, want 0", len(got))
		}
	})

	t.Run("plan order preserved, unknown IDs skipped", func(t *testing.T) {
		m.focusPlan = storage.FocusPlan{Tasks: []string{"p-2", "ghost", "p-1"}}
		got := m.focusTasks()
		if len(got) != 2 || got[0].Meta.ID != "p-2" || got[1].Meta.ID != "p-1" {
			ids := make([]string, len(got))
			for i, task := range got {
				ids[i] = task.Meta.ID
			}
			t.Errorf("got %v, want [p-2 p-1]", ids)
		}
	})
}

func TestSelectedFocusTask(t *testing.T) {
	m := focusFixture(t)
	m.focusPlan = storage.FocusPlan{Tasks: []string{"p-1", "p-2"}}

	t.Run("returns the task under the cursor", func(t *testing.T) {
		m.focusCursor = 1
		if got := m.selectedFocusTask(); got == nil || got.Meta.ID != "p-2" {
			t.Errorf("got %v, want p-2", got)
		}
	})

	t.Run("cursor past the end clamps to the last task", func(t *testing.T) {
		m.focusCursor = 99
		if got := m.selectedFocusTask(); got == nil || got.Meta.ID != "p-2" {
			t.Errorf("got %v, want p-2 (clamped)", got)
		}
	})

	t.Run("empty plan yields nil", func(t *testing.T) {
		m.focusPlan = storage.FocusPlan{}
		if got := m.selectedFocusTask(); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

// TestFocusNavigationSingleStoreReadPerKeypress covers pm-cli-67-2: focusTasks()
// used to call m.store.GetAllTasks() on every invocation, and it is invoked both
// from the key handler (updateFocus) AND the subsequent render (viewFocus) - so
// a single "j" press cost 2 full task-tree reads, defeating reload()'s design of
// one read pass per refresh (see model.go's reload() doc comment). The fix caches
// the ID->task lookup on the Model, rebuilt once in reload(); a keypress + the
// render that follows it should need 0 fresh reads once that cache is warm.
func TestFocusNavigationSingleStoreReadPerKeypress(t *testing.T) {
	base := &storage.Store{Root: t.TempDir()}
	if err := base.CreateProject("p", &storage.Project{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p-1", "p-2", "p-3"} {
		task := &storage.Task{Meta: storage.TaskMeta{ID: id, Title: id, Status: storage.StatusTodo}}
		if err := base.AddTask("p", task); err != nil {
			t.Fatal(err)
		}
	}

	cs := &countingStore{TaskStore: base}
	m := New(cs, "")
	m.focusPlan = storage.FocusPlan{Date: storage.Today(), Tasks: []string{"p-1", "p-2", "p-3"}}
	m.rebuildFocusSet()
	m.currentView = viewFocus

	press := func(key rune) Model {
		cs.getAllTasksCalls = 0
		cs.getTasksCalls = 0
		result, _ := m.updateFocus(keyRunes(key))
		next, ok := result.(Model)
		if !ok {
			t.Fatalf("updateFocus returned %T, want Model", result)
		}
		_ = next.viewFocus() // the render bubbletea performs right after every Update
		if total := cs.getAllTasksCalls + cs.getTasksCalls; total > 1 {
			t.Errorf("key %q triggered %d task-tree reads (GetAllTasks=%d, GetTasks=%d), want at most 1",
				string(key), total, cs.getAllTasksCalls, cs.getTasksCalls)
		}
		return next
	}

	m = press('j')
	m = press('j')
	press('k')
}

func TestFixFocusCursor(t *testing.T) {
	m := focusFixture(t)

	t.Run("cursor past the end snaps to the last task", func(t *testing.T) {
		m.focusPlan = storage.FocusPlan{Tasks: []string{"p-1", "p-2"}}
		m.focusCursor = 5
		m.fixFocusCursor()
		if m.focusCursor != 1 {
			t.Errorf("focusCursor = %d, want 1", m.focusCursor)
		}
	})

	t.Run("no tasks resets the cursor to zero", func(t *testing.T) {
		m.focusPlan = storage.FocusPlan{}
		m.focusCursor = 3
		m.fixFocusCursor()
		if m.focusCursor != 0 {
			t.Errorf("focusCursor = %d, want 0", m.focusCursor)
		}
	})

	t.Run("valid cursor is left alone", func(t *testing.T) {
		m.focusPlan = storage.FocusPlan{Tasks: []string{"p-1", "p-2"}}
		m.focusCursor = 0
		m.fixFocusCursor()
		if m.focusCursor != 0 {
			t.Errorf("focusCursor = %d, want 0 (unchanged)", m.focusCursor)
		}
	})
}
