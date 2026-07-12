package cmd

import (
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestReorderRenumbersSubtasks(t *testing.T) {
	store, slug := tempStore(t)
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1", Title: "Parent", Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-1", Title: "A", Parent: "proj-1", Order: 10, Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-2", Title: "B", Parent: "proj-1", Order: 20, Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-3", Title: "C", Parent: "proj-1", Order: 30, Status: storage.StatusTodo}, "")

	cmd := newReorderCmd(store)
	cmd.SetArgs([]string{"proj-1", "proj-1-3", "proj-1-1", "proj-1-2"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	want := map[string]int{"proj-1-3": 10, "proj-1-1": 20, "proj-1-2": 30}
	for id, wantOrder := range want {
		task, err := store.FindTask(slug, id)
		if err != nil {
			t.Fatalf("find %s: %v", id, err)
		}
		if task.Meta.Order != wantOrder {
			t.Errorf("%s order = %d, want %d", id, task.Meta.Order, wantOrder)
		}
	}

	// And the rollup now lists them in the requested order.
	tasks, _ := store.GetTasks(slug)
	trackers, _ := storage.BuildTrackers(tasks)
	gotIDs := []string{}
	for _, c := range trackers[0].Children {
		gotIDs = append(gotIDs, c.ID)
	}
	wantIDs := []string{"proj-1-3", "proj-1-1", "proj-1-2"}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Errorf("rollup children[%d] = %q, want %q (full %v)", i, gotIDs[i], wantIDs[i], gotIDs)
		}
	}
}

func TestReorderRejectsNonChild(t *testing.T) {
	store, slug := tempStore(t)
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1", Title: "Parent", Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-1", Title: "A", Parent: "proj-1", Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-9", Title: "Stranger", Status: storage.StatusTodo}, "")

	cmd := newReorderCmd(store)
	cmd.SetArgs([]string{"proj-1", "proj-1-1", "proj-9"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-child id, got nil")
	}

	// Nothing should have been written (validation happens before writes).
	a, _ := store.FindTask(slug, "proj-1-1")
	if a.Meta.Order != 0 {
		t.Errorf("proj-1-1 order = %d, want 0 (no write on validation failure)", a.Meta.Order)
	}
}

func TestReorderUnknownParent(t *testing.T) {
	store, _ := tempStore(t)
	cmd := newReorderCmd(store)
	cmd.SetArgs([]string{"proj-404", "proj-404-1"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unknown parent, got nil")
	}
}
