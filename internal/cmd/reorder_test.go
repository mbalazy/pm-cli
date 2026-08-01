package cmd

import (
	"testing"
	"time"

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

// TestReorderSerializesWithProjectLock is the lost-update test for the widest
// unlocked read-modify-write in the codebase (pattern:
// internal/storage/lock_test.go). reorder rewrites SEVERAL children and
// WriteTask persists the whole struct, so an unlocked reorder reverts a
// concurrent locked write - brief and body included.
//
// Deterministic by construction: the test holds the project lock, starts the
// reorder (which scans, then must BLOCK on the lock), edits a child while
// still holding it, and only then releases. A reorder that ignores the lock,
// or takes it but keeps writing the copies it scanned BEFORE the critical
// section, clobbers the edit; a correct one blocks and re-reads fresh.
func TestReorderSerializesWithProjectLock(t *testing.T) {
	store, slug := tempStore(t)
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1", Title: "Parent", Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-1", Title: "A", Parent: "proj-1", Order: 10, Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-2", Title: "B", Parent: "proj-1", Order: 20, Status: storage.StatusTodo}, "")

	release, err := store.LockProject(slug)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Deferred as well as released explicitly below (the closure is
	// sync.Once): a t.Fatal between here and there exits via runtime.Goexit
	// while still holding the flock, leaving the reorder goroutine blocked on
	// it and the fd open for the life of the test binary.
	defer release()

	done := make(chan error, 1)
	go func() {
		cmd := newReorderCmd(store)
		cmd.SetArgs([]string{"proj-1", "proj-1-2", "proj-1-1"})
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		done <- cmd.Execute()
	}()

	// The reorder must not have written anything yet - it is waiting on us.
	select {
	case err := <-done:
		release()
		t.Fatalf("reorder finished while the project lock was held (err = %v)", err)
	case <-time.After(200 * time.Millisecond):
	}

	// "Another session" edits a child inside our critical section.
	child, err := store.FindTask(slug, "proj-1-1")
	if err != nil {
		t.Fatal(err)
	}
	child.Meta.Brief = "fresh brief"
	child.Body = "fresh note"
	if err := store.WriteTask(child); err != nil {
		t.Fatal(err)
	}
	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reorder: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reorder never finished after the lock was released")
	}

	final, err := store.FindTask(slug, "proj-1-1")
	if err != nil {
		t.Fatal(err)
	}
	if final.Meta.Brief != "fresh brief" || final.Body != "fresh note" {
		t.Fatalf("reorder clobbered a concurrent edit: brief=%q body=%q", final.Meta.Brief, final.Body)
	}
	if final.Meta.Order != 20 {
		t.Fatalf("proj-1-1 order = %d, want 20 (the reorder must still apply)", final.Meta.Order)
	}
	other, err := store.FindTask(slug, "proj-1-2")
	if err != nil {
		t.Fatal(err)
	}
	if other.Meta.Order != 10 {
		t.Fatalf("proj-1-2 order = %d, want 10", other.Meta.Order)
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
