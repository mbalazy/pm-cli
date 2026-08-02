package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func runAddCmd(store storage.TaskStore, args ...string) error {
	cmd := newAddCmd(store)
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd.Execute()
}

// TestAddMintsUniqueIDsUnderConcurrency encodes the duplicate-ID race:
// NextTaskID derives the next number from the tasks on disk, so two unlocked
// adds read the same max and mint the SAME id. O_EXCL does not save it - it
// protects the file NAME ("<id>-<title-slug>.md"), and these titles differ, so
// both writes succeed and one of the twins becomes unaddressable (findByExactID
// returns whichever ReadDir yields first). Measured before the fix: six
// parallel adds produced six files carrying two distinct IDs.
func TestAddMintsUniqueIDsUnderConcurrency(t *testing.T) {
	store, slug := tempStore(t)

	const adders = 8
	// Start barrier: releasing the goroutines together maximizes the overlap
	// inside NextTaskID. It does NOT make detection certain - at -cpu=1 they
	// still run largely serially, and a reverted fix passes. The deterministic
	// detector is TestAddBlocksOnProjectLockAndRereads; this one is the
	// property test (N adds -> N distinct ids).
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < adders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if err := runAddCmd(store, slug, fmt.Sprintf("Parallel task %d", i)); err != nil {
				t.Errorf("add %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	tasks, err := store.GetTasks(slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != adders {
		t.Fatalf("wrote %d task file(s), want %d", len(tasks), adders)
	}
	seen := map[string]string{}
	for _, tk := range tasks {
		if prev, dup := seen[tk.Meta.ID]; dup {
			t.Errorf("duplicate id %q: %q and %q", tk.Meta.ID, prev, tk.Meta.Title)
		}
		seen[tk.Meta.ID] = tk.Meta.Title
	}
	if len(seen) != adders {
		t.Fatalf("%d distinct id(s) for %d adds: %v", len(seen), adders, seen)
	}
}

// TestAddBlocksOnProjectLockAndRereads is the DETERMINISTIC half of AC-1.
// TestAddMintsUniqueIDsUnderConcurrency is a property test and its detection
// is probabilistic - measured with the lock removed it catches the duplicate
// 10/10 at the default GOMAXPROCS but 0/30 at -cpu=1, where the goroutines
// simply run serially through NextTaskID. This one blocks on the real flock
// instead of on the scheduler (same shape as TestReorderSerializesWithProjectLock):
// the test holds the lock, so a locking `pm add` CANNOT finish, and once
// released it must mint its ID from the state as of AFTER the wait.
func TestAddBlocksOnProjectLockAndRereads(t *testing.T) {
	store, slug := tempStore(t)

	release, err := store.LockProject(slug)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer release() // see TestReorderSerializesWithProjectLock - Once-idempotent

	done := make(chan error, 1)
	go func() { done <- runAddCmd(store, slug, "Waited for the lock") }()

	select {
	case err := <-done:
		t.Fatalf("`pm add` completed while the project lock was held (err = %v) - it minted its id from unserialized state", err)
	case <-time.After(200 * time.Millisecond):
	}

	// Consume the id it would have taken, inside the critical section.
	if err := store.AddTask(slug, &storage.Task{
		Meta: storage.TaskMeta{ID: slug + "-1", Title: "Taken meanwhile", Status: storage.StatusTodo},
	}); err != nil {
		t.Fatal(err)
	}
	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("add: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("`pm add` never finished after the lock was released")
	}

	tasks, err := store.GetTasks(slug)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, tk := range tasks {
		if prev, dup := ids[tk.Meta.ID]; dup {
			t.Fatalf("duplicate id %q: %q and %q - the add did not re-read inside the lock", tk.Meta.ID, prev, tk.Meta.Title)
		}
		ids[tk.Meta.ID] = tk.Meta.Title
	}
	if ids[slug+"-2"] != "Waited for the lock" {
		t.Fatalf("the waiting add minted from stale state: ids = %v", ids)
	}
}

// TestAddRejectsTraversingID: --id is raw user input that becomes the leading
// component of the task's file name. Before the storage-level check,
// `pm add demo x --id ../../escaped` reported SUCCESS and wrote the file two
// directories above the pm root, where nothing ever reads it back.
func TestAddRejectsTraversingID(t *testing.T) {
	store, slug := tempStore(t)
	root := store.RootDir()
	outside := filepath.Dir(root)

	for _, id := range []string{"../escaped", "../../escaped", "sub/dir"} {
		err := runAddCmd(store, slug, "Escaping task", "--id", id)
		if err == nil {
			t.Fatalf("--id %q was accepted", id)
		}
		if !strings.Contains(err.Error(), "invalid task id") {
			t.Errorf("--id %q: want a ValidateTaskID error, got: %v", id, err)
		}
	}

	tasks, err := store.GetTasks(slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("rejected adds created %d task(s)", len(tasks))
	}
	for _, dir := range []string{root, outside} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.Contains(e.Name(), "escaping") || strings.Contains(e.Name(), "escaped") {
				t.Fatalf("task file escaped to %s", filepath.Join(dir, e.Name()))
			}
		}
	}
}
