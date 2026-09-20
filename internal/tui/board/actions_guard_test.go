package board

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// TestDoMoveGuardsEmptyStatuses covers the reproduced panic (pm-cli-74-2):
// doMoveForward/doMoveBack indexed m.statuses without a length guard, so
// hiding every column (the V menu) and then pressing m/M panicked with
// "integer divide by zero" (forward, via the modulo) or "index out of range
// [-1]" (back). The fix guards INSIDE doMoveForward/doMoveBack themselves, so
// every entry point is safe - including update_detail.go's and
// update_modes.go's updateFocus, which never had their own per-site guard.
func TestDoMoveGuardsEmptyStatuses(t *testing.T) {
	hideAllColumns := func(t *testing.T, m *Model) {
		t.Helper()
		for _, s := range storage.DefaultStatuses {
			m.hiddenStatuses[s] = true
		}
		m.reload()
		if len(m.statuses) != 0 {
			t.Fatalf("precondition: m.statuses should be empty, got %v", m.statuses)
		}
	}

	t.Run("doMoveForward direct call does not panic and toasts", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		task := m.taskByID("p-1")
		hideAllColumns(t, m)

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("doMoveForward panicked: %v", r)
				}
			}()
			m.doMoveForward(task)
		}()
		if m.toastMsg != "no visible columns" {
			t.Errorf("toastMsg = %q, want 'no visible columns'", m.toastMsg)
		}
		if got := diskStatus(t, m, "p-1"); got != storage.StatusTodo {
			t.Errorf("status = %q, want unchanged (todo)", got)
		}
	})

	t.Run("doMoveBack direct call does not panic and toasts", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDoing}})
		task := m.taskByID("p-1")
		hideAllColumns(t, m)

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("doMoveBack panicked: %v", r)
				}
			}()
			m.doMoveBack(task)
		}()
		if m.toastMsg != "no visible columns" {
			t.Errorf("toastMsg = %q, want 'no visible columns'", m.toastMsg)
		}
		if got := diskStatus(t, m, "p-1"); got != storage.StatusDoing {
			t.Errorf("status = %q, want unchanged (doing)", got)
		}
	})

	t.Run("detail view m does not panic with zero columns", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		hideAllColumns(t, m)
		m.detailTask = m.taskByID("p-1")
		m.currentView = viewDetail
		m.previousView = viewBoard

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("detail key %q panicked: %v", "m", r)
				}
			}()
			res, _ := m.updateDetail(keyMsg("m"))
			m2, ok := res.(Model)
			if !ok {
				t.Fatalf("updateDetail returned %T, want Model", res)
			}
			if !strings.Contains(m2.toastMsg, "no visible columns") {
				t.Errorf("toastMsg = %q, want 'no visible columns'", m2.toastMsg)
			}
		}()
	})

	t.Run("detail view M does not panic with zero columns", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDoing}})
		hideAllColumns(t, m)
		m.detailTask = m.taskByID("p-1")
		m.currentView = viewDetail
		m.previousView = viewBoard

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("detail key %q panicked: %v", "M", r)
				}
			}()
			res, _ := m.updateDetail(keyMsg("M"))
			m2, ok := res.(Model)
			if !ok {
				t.Fatalf("updateDetail returned %T, want Model", res)
			}
			if !strings.Contains(m2.toastMsg, "no visible columns") {
				t.Errorf("toastMsg = %q, want 'no visible columns'", m2.toastMsg)
			}
		}()
	})

	t.Run("focus view m does not panic with zero columns", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		hideAllColumns(t, m)
		m.focusPlan.Tasks = []string{"p-1"}
		m.rebuildFocusSet()
		m.focusCursor = 0

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("focus key %q panicked: %v", "m", r)
				}
			}()
			res, _ := m.updateFocus(keyMsg("m"))
			m2, ok := res.(Model)
			if !ok {
				t.Fatalf("updateFocus returned %T, want Model", res)
			}
			if !strings.Contains(m2.toastMsg, "no visible columns") {
				t.Errorf("toastMsg = %q, want 'no visible columns'", m2.toastMsg)
			}
		}()
	})

	t.Run("focus view M does not panic with zero columns", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusDoing}})
		hideAllColumns(t, m)
		m.focusPlan.Tasks = []string{"p-1"}
		m.rebuildFocusSet()
		m.focusCursor = 0

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("focus key %q panicked: %v", "M", r)
				}
			}()
			res, _ := m.updateFocus(keyMsg("M"))
			m2, ok := res.(Model)
			if !ok {
				t.Fatalf("updateFocus returned %T, want Model", res)
			}
			if !strings.Contains(m2.toastMsg, "no visible columns") {
				t.Errorf("toastMsg = %q, want 'no visible columns'", m2.toastMsg)
			}
		}()
	})
}
