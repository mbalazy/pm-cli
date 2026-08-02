package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// makeFocusPlanUnwritable puts a directory where focus.yaml would be
// written, so storage.WriteFocusPlan (and therefore saveFocusPlan) fails.
func makeFocusPlanUnwritable(t *testing.T, m *Model) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(m.store.RootDir(), "focus.yaml"), 0755); err != nil {
		t.Fatal(err)
	}
}

// TestFocusToastNotClobberedByFailedSave covers a fresh-review finding on
// pm-cli-74-2: saveFocusPlan's error toast (15s expiry) was correctly set,
// but three call sites unconditionally overwrote toastMsg/toastExpiry with
// their own success message right after - silently discarding the error the
// fix was supposed to surface. saveFocusPlan now reports success so callers
// can skip their own toast on failure.
func TestFocusToastNotClobberedByFailedSave(t *testing.T) {
	assertErrorToastSurvived := func(t *testing.T, toastMsg string, toastExpiry time.Time) {
		t.Helper()
		if !strings.Contains(toastMsg, "focus plan") {
			t.Errorf("toastMsg = %q, want it to mention the focus plan write failure, not a clobbering success message", toastMsg)
		}
		if time.Until(toastExpiry) < 10*time.Second {
			t.Errorf("toastExpiry implies %v left, want the ~15s error timing (not a shortened 2s/4s success timing)", time.Until(toastExpiry))
		}
	}

	t.Run("focus view Focus/Delete key (t/x)", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		m.focusPlan.Tasks = []string{"p-1"}
		m.rebuildFocusSet()
		m.focusCursor = 0
		makeFocusPlanUnwritable(t, m)

		result, _ := m.updateFocus(keyMsg("t"))
		m2, ok := result.(Model)
		if !ok {
			t.Fatalf("updateFocus returned %T, want Model", result)
		}
		assertErrorToastSurvived(t, m2.toastMsg, m2.toastExpiry)
	})

	t.Run("board Focus toggle key (t)", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		makeFocusPlanUnwritable(t, m)

		result, _ := m.updateBoard(keyMsg("t"))
		m2, ok := result.(Model)
		if !ok {
			t.Fatalf("updateBoard returned %T, want Model", result)
		}
		assertErrorToastSurvived(t, m2.toastMsg, m2.toastExpiry)
	})

	t.Run("handleStalePlan carry-over", func(t *testing.T) {
		m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})
		m.focusPlan = storage.FocusPlan{Date: "2020-01-01", Tasks: []string{"p-1"}}
		makeFocusPlanUnwritable(t, m)

		m.handleStalePlan()
		assertErrorToastSurvived(t, m.toastMsg, m.toastExpiry)
	})
}
