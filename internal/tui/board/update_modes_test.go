package board

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
)

// TestSelectModeDoneHiddenColumn is the bulk (select-mode) sibling of the
// pm-cli-53 regression in actions_test.go: pressing "d" with the done column
// hidden used to write m.statuses[len-1] (the last VISIBLE column) to every
// marked task instead of the project's real done status.
func TestSelectModeDoneHiddenColumn(t *testing.T) {
	m := newBoardModel(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}},
		&storage.Task{Meta: storage.TaskMeta{ID: "p-2", Title: "B", Status: storage.StatusTodo}},
	)
	m.hiddenStatuses[storage.StatusDone] = true
	m.reload()
	if got := m.statuses[len(m.statuses)-1]; got == storage.StatusDone {
		t.Fatalf("precondition: done column should be hidden from m.statuses, got last visible = %q", got)
	}

	m.selecting = true
	m.selected = map[string]bool{"p-1": true, "p-2": true}

	result, _ := m.updateSelectMode(keyMsg("d"))
	got, ok := result.(Model)
	if !ok {
		t.Fatalf("updateSelectMode returned %T, want Model", result)
	}

	for _, id := range []string{"p-1", "p-2"} {
		if status := diskStatus(t, &got, id); status != storage.StatusDone {
			t.Errorf("%s status = %q, want done even with the done column hidden", id, status)
		}
	}
}

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

// TestSelectModeBulkActionsWithMarkedTaskAndNoColumns: m (move forward) and M
// (move back) build the next status via m.statuses[...] inside a closure
// passed to bulkStatusMove - unguarded against m.statuses being empty. Not
// reachable through the live TUI today (the column-visibility menu only opens
// from board mode, and every exit from select mode clears m.selected first,
// so a marked task can't coexist with zero visible columns) - guarded anyway
// since bulkStatusMove has no visibility into column state and a future
// change to either invariant would panic immediately. d resolves the done
// status from the store (never empty), so it must survive zero columns
// without a guard.
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

// TestArchiveSearchKeyDoesNotLeakIntoBoard is the pm-cli-65 regression:
// pressing "/" in the archive view set m.searching without the archive
// dispatch ever consuming search-mode keys, so the flag survived the esc
// back to the board and the very next keypress there was silently swallowed
// by updateSearch instead of updateBoard. Drives the real Model.Update
// dispatch (not updateArchive directly) so the routing bug is exercised.
func TestArchiveSearchKeyDoesNotLeakIntoBoard(t *testing.T) {
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "A", Status: storage.StatusTodo}})

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	got, ok := result.(Model)
	if !ok {
		t.Fatalf("Update(ctrl+a) returned %T, want Model", result)
	}
	if got.currentView != viewArchive {
		t.Fatalf("currentView = %v, want viewArchive after ctrl+a", got.currentView)
	}

	result, _ = got.Update(keyMsg("/"))
	got, ok = result.(Model)
	if !ok {
		t.Fatalf("Update(/) returned %T, want Model", result)
	}
	if got.searching {
		t.Errorf("searching = true after / in archive view, want a no-op (archive has no filter)")
	}
	if got.currentView != viewArchive {
		t.Errorf("currentView = %v after / in archive view, want to stay in archive", got.currentView)
	}

	result, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got, ok = result.(Model)
	if !ok {
		t.Fatalf("Update(esc) returned %T, want Model", result)
	}
	if got.currentView != viewBoard {
		t.Fatalf("currentView = %v, want viewBoard after esc from archive", got.currentView)
	}
	if got.searching {
		t.Fatalf("searching = true after archive -> board, want no unrequested search prompt")
	}
}

// TestArchivedTasksIgnoresSearchQuery covers the still-live half of pm-cli-65:
// batch A (abab6e3) fixed only the "/" MODE leak into the archive view -
// archivedTasks() itself still filtered by m.searchQuery, so a filter left
// active on the board from before Ctrl+a silently pre-filtered the archive
// list even though the archive has no filter feature of its own. A matching
// task must stay visible in the archive regardless of a non-matching board
// query, and the board's own query must survive the round trip unchanged.
func TestArchivedTasksIgnoresSearchQuery(t *testing.T) {
	// StatusArchived is system-level, not one of the project's own statuses
	// (see storage.ValidateStatus), so AddTask cannot create a task with it
	// directly - archive via MoveTask like the rest of the board does.
	m := newBoardModel(t, &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Archived one", Status: storage.StatusTodo}})
	task := m.taskByID("p-1")
	if err := m.store.MoveTask(task, storage.StatusArchived); err != nil {
		t.Fatal(err)
	}
	m.reload()
	m.searchQuery = "does-not-match-anything"

	tasks := m.archivedTasks()
	if len(tasks) != 1 || tasks[0].Meta.ID != "p-1" {
		t.Fatalf("archivedTasks() = %v, want [p-1] despite a non-matching board searchQuery", tasks)
	}
	if m.searchQuery != "does-not-match-anything" {
		t.Errorf("searchQuery = %q, want the board's filter left untouched", m.searchQuery)
	}
}
