package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
	// Start barrier: without it the goroutines merely tend to overlap inside
	// NextTaskID, so a reverted fix could pass green on a constrained runner
	// (GOMAXPROCS=1 plus an unlucky schedule). Releasing them all at once
	// makes the collision deterministic in practice.
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
