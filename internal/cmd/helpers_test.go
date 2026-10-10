package cmd

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// These helpers have twins in internal/executor's tests: test files cannot be
// imported across packages, and both packages build stores and read stdout.

// capturePipe points *target at a pipe for the duration of fn and returns what
// fn wrote. The pipe is drained by a concurrent reader: a fixture writing more
// than the OS pipe buffer (~64KB) would otherwise block fn forever and hang the
// whole test binary instead of failing an assertion. Restore + close go through
// a deferred sync.Once so a t.Fatal or panic inside fn cannot leave the stream
// pointing at a dead pipe for every later test in the package.
func capturePipe(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	old := *target
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
			*target = old
			w.Close()
		})
	}
	*target = w
	defer restore()
	fn()
	restore()
	return <-drained
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return capturePipe(t, &os.Stdout, fn)
}

func tempStore(t *testing.T) (*storage.Store, string) {
	t.Helper()
	root := t.TempDir()
	store := &storage.Store{Root: root}
	if err := store.CreateProject("proj", &storage.Project{Name: "Proj", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return store, "proj"
}

func addTask(t *testing.T, store *storage.Store, slug string, meta storage.TaskMeta, body string) *storage.Task {
	t.Helper()
	task := &storage.Task{Meta: meta, Body: body}
	if err := store.AddTask(slug, task); err != nil {
		t.Fatalf("add task %s: %v", meta.ID, err)
	}
	return task
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q\n---\n%s", needle, haystack)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
