package board

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
	"github.com/mbalazy/pm-cli/internal/storage"
)

func keyRunes(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// TestBoardKeysWithAllColumnsHidden: hiding every column (colVis menu) leaves
// m.statuses and m.cursors empty with activeCol 0 - the cursor-jump keys used
// to index m.cursors unguarded and panicked the whole board.
func TestBoardKeysWithAllColumnsHidden(t *testing.T) {
	m := Model{
		statuses:      nil,
		cursors:       []int{},
		scrollOffsets: []int{},
		width:         80, height: 24,
	}

	// g (JumpTop) and ctrl+u (HalfUp) were the unguarded ones; G/ctrl+d/j/k
	// were already guarded - keep them covered so a refactor can't regress any.
	msgs := []tea.KeyMsg{
		keyRunes('g'),
		keyRunes('G'),
		keyRunes('j'),
		keyRunes('k'),
		{Type: tea.KeyCtrlU},
		{Type: tea.KeyCtrlD},
	}
	for _, msg := range msgs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("key %q panicked: %v", msg.String(), r)
				}
			}()
			m.updateBoard(msg)
		}()
	}
}

// TestAddWithAllColumnsHidden covers pm-cli-67-1: applyColumnVisibility (the V
// menu) can legally empty m.statuses, and the Add flow indexed m.statuses[0]
// unguarded - both at the "a" keybinding (update.go) and again at task
// creation time (updateAdd, update_menus.go). Confirmed panic before the fix:
// "runtime error: index out of range [0] with length 0".
func TestAddWithAllColumnsHidden(t *testing.T) {
	t.Run("the a keybinding does not enter add mode", func(t *testing.T) {
		m := Model{
			projects:      []string{"all", "p"},
			activeProject: 1,
			statuses:      nil,
			cursors:       []int{},
			scrollOffsets: []int{},
			width:         80, height: 24,
		}
		result, _ := m.updateBoard(keyRunes('a'))
		m2 := result.(Model)
		if m2.adding {
			t.Error("add mode should not open with zero visible columns")
		}
		if !strings.Contains(m2.toastMsg, "no visible columns") {
			t.Errorf("toastMsg = %q, want a hint about hidden columns", m2.toastMsg)
		}
	})

	t.Run("Enter during add does not index an emptied m.statuses", func(t *testing.T) {
		// Simulates hiddenStatuses changing out from under an already-open add
		// dialog (defense in depth - the "a" guard above is the normal path).
		m := Model{
			projects:      []string{"all", "p"},
			activeProject: 1,
			statuses:      nil,
			adding:        true,
			addStep:       1,
			addTitle:      "New task",
			width:         80, height: 24,
		}

		var m2 Model
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Enter with empty m.statuses panicked: %v", r)
				}
			}()
			result, _ := m.updateAdd(tea.KeyMsg{Type: tea.KeyEnter})
			m2 = result.(Model)
		}()
		if m2.adding {
			t.Error("add mode should close instead of creating a task with no status")
		}
	})
}

// TestDeleteTaskShowsErrorToast covers pm-cli-67-1: DeleteTask's error was
// dropped on the floor (MoveTask's hardening never reached its sibling
// mutations), so a failed delete looked identical to a successful one. A task
// with no FilePath is store.DeleteTask's own documented failure mode
// ("task has no file path").
func TestDeleteTaskShowsErrorToast(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "No file", Status: storage.StatusTodo}}

	m := Model{
		store:         store,
		tasks:         []*storage.Task{task},
		statuses:      []storage.TaskStatus{storage.StatusTodo},
		cursors:       []int{0},
		scrollOffsets: []int{0},
		width:         80, height: 24,
	}

	// First X arms the confirmation, second X (matching confirmTaskID) executes
	// it. Uppercase since 0.45.0: lowercase x launches the executor, matching c.
	result, _ := m.updateBoard(keyRunes('X'))
	m2 := result.(Model)
	result, _ = m2.updateBoard(keyRunes('X'))
	m3 := result.(Model)

	if !strings.Contains(m3.toastMsg, "delete failed") || !strings.Contains(m3.toastMsg, "no file path") {
		t.Errorf("toastMsg = %q, want it to mention the delete failure and the reason", m3.toastMsg)
	}
}

// TestSessionMenuActsOnDetailTask: the session menu opens from the DETAIL view
// on m.detailTask; after a relation jump the board cursor points at a DIFFERENT
// task. x (delete session) used selectedTask() and deleted the session from
// that other task.
func TestSessionMenuActsOnDetailTask(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("p", &storage.Project{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	cursorTask := &storage.Task{Meta: storage.TaskMeta{
		ID: "p-1", Title: "Under board cursor", Status: storage.StatusTodo,
		Sessions: []string{"sess-cursor"},
	}}
	detailTask := &storage.Task{Meta: storage.TaskMeta{
		ID: "p-2", Title: "Open in detail", Status: storage.StatusTodo,
		Sessions: []string{"sess-detail"},
	}}
	for _, tk := range []*storage.Task{cursorTask, detailTask} {
		tk.FilePath = filepath.Join(store.ProjectDir("p"), tk.Meta.ID+".md")
		if err := store.AddTask("p", tk); err != nil {
			t.Fatal(err)
		}
	}

	m := Model{
		store:       store,
		projects:    []string{"all", "p"},
		tasks:       []*storage.Task{cursorTask, detailTask},
		statuses:    []storage.TaskStatus{storage.StatusTodo},
		cursors:     []int{0}, // board cursor sits on p-1
		currentView: viewDetail,
		width:       80,
		height:      24,
		detailState: detailState{
			detailTask: detailTask,
		},
	}
	if got := m.selectedTask(); got == nil || got.Meta.ID != "p-1" {
		t.Fatalf("fixture: board cursor should sit on p-1, got %v", got)
	}

	m.openSessionMenu(detailTask)
	if !m.sessionMenu || len(m.sessionMenuItems) != 1 {
		t.Fatalf("session menu should list p-2's one session: %+v", m.sessionMenuItems)
	}

	// x twice = delete with confirmation.
	next, _ := m.updateSessionMenu(keyRunes('x'))
	next, _ = next.(Model).updateSessionMenu(keyRunes('x'))
	_ = next

	freshDetail, _ := store.FindTask("p", "p-2")
	if len(freshDetail.Meta.Sessions) != 0 {
		t.Errorf("p-2's session should be deleted, got %v", freshDetail.Meta.Sessions)
	}
	freshCursor, _ := store.FindTask("p", "p-1")
	if len(freshCursor.Meta.Sessions) != 1 || freshCursor.Meta.Sessions[0] != "sess-cursor" {
		t.Errorf("p-1 (board cursor task) must be untouched, got %v", freshCursor.Meta.Sessions)
	}
}

func TestTruncateWidth(t *testing.T) {
	cases := []struct {
		name string
		s    string
		max  int
	}{
		{"ascii over", strings.Repeat("x", 50), 10},
		{"polish diacritics", "zażółć gęślą jaźń pchnięta w tło", 10},
		{"emoji double width", "🔥🔥🔥🔥🔥🔥", 5},
		{"tiny max clamps", "whatever long string", -3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := truncateWidth(c.s, c.max)
			if !utf8.ValidString(got) {
				t.Fatalf("result is not valid UTF-8: %q", got)
			}
			if !strings.HasSuffix(got, "…") {
				t.Fatalf("truncated result should end with ellipsis: %q", got)
			}
			// The point of the function: the result fits the column it was cut
			// for. A byte-oriented cut overshoots on diacritics and doubly so
			// on emoji, which is what pushed the menus out of their overlay.
			if w, limit := runewidth.StringWidth(got), max(c.max, 2); w > limit {
				t.Errorf("result is %d cells wide, want at most %d: %q", w, limit, got)
			}
		})
	}
	if got := truncateWidth("short", 10); got != "short" {
		t.Errorf("under-limit string must pass through, got %q", got)
	}
}
