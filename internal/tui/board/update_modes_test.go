package board

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
)

// TestSelectModeKeysWithAllColumnsHidden: same panic class as
// TestBoardKeysWithAllColumnsHidden (update_board_test.go) but for select
// mode - hiding every column leaves m.statuses and m.cursors empty with
// activeCol 0, and updateSelectMode indexed m.cursors[m.activeCol]
// unguarded on j/k/g.
func TestSelectModeKeysWithAllColumnsHidden(t *testing.T) {
	m := Model{
		statuses:      nil,
		cursors:       []int{},
		scrollOffsets: []int{},
		selecting:     true,
		selected:      make(map[string]bool),
		width:         80, height: 24,
	}

	msgs := []tea.KeyMsg{
		keyRunes('g'),
		keyRunes('G'),
		keyRunes('j'),
		keyRunes('k'),
		keyRunes('h'),
		keyRunes('l'),
	}
	for _, msg := range msgs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("key %q panicked: %v", msg.String(), r)
				}
			}()
			m.updateSelectMode(msg)
		}()
	}
}

// TestSelectModeBulkActionsWithMarkedTaskAndNoColumns: m (move forward), M
// (move back) and d (mark done) each build the next status via
// m.statuses[...] inside a closure passed to bulkStatusMove - unguarded
// against m.statuses being empty. Not reachable through the live TUI today
// (the column-visibility menu only opens from board mode, and every exit
// from select mode clears m.selected first, so a marked task can't coexist
// with zero visible columns) - guarded anyway since bulkStatusMove has no
// visibility into column state and a future change to either invariant
// would panic immediately.
func TestSelectModeBulkActionsWithMarkedTaskAndNoColumns(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("p", &storage.Project{Name: "P"}); err != nil {
		t.Fatal(err)
	}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "T", Status: storage.StatusTodo}}
	task.FilePath = store.ProjectDir("p") + "/p-1.md"
	if err := store.AddTask("p", task); err != nil {
		t.Fatal(err)
	}

	base := Model{
		store:     store,
		projects:  []string{"all", "p"},
		tasks:     []*storage.Task{task},
		statuses:  nil, // zero visible columns
		cursors:   []int{},
		selecting: true,
		width:     80, height: 24,
	}

	for _, k := range []rune{'m', 'M', 'd'} {
		mm := base
		mm.selected = map[string]bool{"p-1": true} // pre-marked, bypassing selectedTask()'s nil guard
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("key %q panicked: %v", string(k), r)
				}
			}()
			mm.updateSelectMode(keyRunes(k))
		}()
	}
}
