package service

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// captureStderr swaps os.Stderr for a pipe while fn runs and returns what fn
// wrote to it (the internal/storage helper of the same name, which this
// package cannot import from a test file).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	drained := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		r.Close()
		drained <- string(out)
	}()
	var once sync.Once
	restore := func() {
		once.Do(func() {
			os.Stderr = old
			w.Close()
		})
	}
	os.Stderr = w
	defer restore()
	fn()
	restore()
	return <-drained
}

// TestTaskWritesWarnWhenTheLockIsUnavailable: a task write that cannot take
// the project lock still writes (best-effort, like storage's own mutations)
// but says on stderr that it went ahead unlocked - otherwise a concurrent
// session's edit can be lost with no trace. With the lock available nothing is
// printed.
func TestTaskWritesWarnWhenTheLockIsUnavailable(t *testing.T) {
	ops := []struct {
		name   string
		op     string // the operation the warning names
		run    func(store *storage.Store) error
		verify func(t *testing.T, store *storage.Store)
	}{
		{
			name: "add",
			op:   "adding task",
			run: func(store *storage.Store) error {
				_, err := AddTask(store, AddTaskInput{Project: "test", Title: "Second"})
				return err
			},
			verify: func(t *testing.T, store *storage.Store) {
				if _, err := store.FindTaskExact("test", "t-2"); err != nil {
					t.Fatalf("task not written: %v", err)
				}
			},
		},
		{
			name: "update",
			op:   "updating task t-1",
			run: func(store *storage.Store) error {
				_, err := UpdateTask(store, UpdateTaskInput{Project: "test", TaskID: "t-1", Brief: strp("new brief")})
				return err
			},
			verify: func(t *testing.T, store *storage.Store) {
				task, err := store.FindTaskExact("test", "t-1")
				if err != nil || task.Meta.Brief != "new brief" {
					t.Fatalf("update not written: %+v, %v", task, err)
				}
			},
		},
		{
			name: "delete",
			op:   "deleting task t-1",
			run: func(store *storage.Store) error {
				_, err := DeleteTask(store, DeleteTaskInput{Project: "test", TaskID: "t-1"})
				return err
			},
			verify: func(t *testing.T, store *storage.Store) {
				if _, err := store.FindTaskExact("test", "t-1"); err == nil {
					t.Fatal("task not deleted")
				}
			},
		},
	}

	for _, tc := range ops {
		t.Run(tc.name+" without the lock", func(t *testing.T) {
			store := newTestStore(t)
			// A directory where the lock file belongs makes LockProject's
			// open fail, without root and without touching the task files.
			if err := os.Mkdir(filepath.Join(store.ProjectDir("test"), ".pm.lock"), 0o755); err != nil {
				t.Fatal(err)
			}
			var runErr error
			out := captureStderr(t, func() { runErr = tc.run(store) })
			if runErr != nil {
				t.Fatalf("the write must go ahead unlocked: %v", runErr)
			}
			tc.verify(t, store)
			want := `pm: project lock unavailable for "test" (`
			if !strings.Contains(out, want) || !strings.Contains(out, ") - "+tc.op+" without it\n") {
				t.Fatalf("stderr %q, want the lock-degrade warning naming %q", out, tc.op)
			}
		})

		t.Run(tc.name+" with the lock", func(t *testing.T) {
			store := newTestStore(t)
			var runErr error
			out := captureStderr(t, func() { runErr = tc.run(store) })
			if runErr != nil {
				t.Fatal(runErr)
			}
			tc.verify(t, store)
			if out != "" {
				t.Fatalf("nothing belongs on stderr when the lock is taken, got %q", out)
			}
		})
	}
}
