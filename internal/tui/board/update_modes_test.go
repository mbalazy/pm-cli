package board

import (
	"testing"

	"github.com/mbalazy/pm/internal/storage"
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
