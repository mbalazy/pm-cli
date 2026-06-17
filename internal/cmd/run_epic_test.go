package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// tempStore creates an isolated store with one project ("proj") whose path is a
// temp dir, and returns the store + slug.
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

func TestReadySubs(t *testing.T) {
	store, slug := tempStore(t)
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1", Title: "Tracker", Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-2", Title: "B", Status: storage.StatusTodo, Parent: "proj-1", Order: 2}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-1", Title: "A", Status: storage.StatusTodo, Parent: "proj-1", Order: 1}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-3", Title: "C", Status: storage.StatusTodo, Parent: "proj-1", Order: 3}, "")
	// unrelated task with a different parent must be excluded
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-2", Title: "Other", Status: storage.StatusTodo, Parent: "proj-9"}, "")

	subs, err := readySubs(store, slug, "proj-1")
	if err != nil {
		t.Fatalf("readySubs: %v", err)
	}
	got := make([]string, len(subs))
	for i, s := range subs {
		got[i] = s.Meta.ID
	}
	want := []string{"proj-1-1", "proj-1-2", "proj-1-3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("readySubs order = %v, want %v", got, want)
	}
}

func TestRecordCrossCutting(t *testing.T) {
	store, slug := tempStore(t)

	t.Run("appends Manager Notes section into the spec", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-10", Title: "Epic", Status: storage.StatusDoing},
			storage.SpecStart+"\n## Description\nbuild it\n"+storage.SpecEnd+"\n\nlog tail")

		if err := recordCrossCutting(store, parent, "proj-10-1", []string{"streak store seam"}); err != nil {
			t.Fatalf("recordCrossCutting: %v", err)
		}
		// second finding from a later sub accumulates
		if err := recordCrossCutting(store, parent, "proj-10-2", []string{"nav handoff"}); err != nil {
			t.Fatalf("recordCrossCutting 2: %v", err)
		}

		reloaded, err := store.FindTask(slug, "proj-10")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		spec := storage.ExtractSpec(reloaded.Body)
		mustContain(t, spec, managerNotesHeading)
		mustContain(t, spec, "[proj-10-1] streak store seam")
		mustContain(t, spec, "[proj-10-2] nav handoff")
		mustContain(t, spec, "## Description")
		// only one Manager Notes heading despite two appends
		if c := strings.Count(spec, managerNotesHeading); c != 1 {
			t.Errorf("expected 1 Manager Notes heading, got %d", c)
		}
		// log tail outside the spec preserved
		mustContain(t, reloaded.Body, "log tail")
	})

	t.Run("falls back to Log when parent has no spec", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-11", Title: "Epic2", Status: storage.StatusDoing}, "plain body, no markers")
		if err := recordCrossCutting(store, parent, "proj-11-1", []string{"finding x"}); err != nil {
			t.Fatalf("recordCrossCutting: %v", err)
		}
		reloaded, _ := store.FindTask(slug, "proj-11")
		if storage.ExtractSpec(reloaded.Body) != "" {
			t.Error("did not expect a spec block to be created")
		}
		mustContain(t, reloaded.Body, "[proj-11-1] finding x")
		mustContain(t, reloaded.Body, "plain body, no markers")
	})
}

func TestManagerNoteBlock(t *testing.T) {
	got := managerNoteBlock("s-1", []string{"a", "b"})
	want := "- [s-1] a\n- [s-1] b"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAnyMerged(t *testing.T) {
	if anyMerged([]subOutcome{{result: "blocked"}, {result: "skipped"}}) {
		t.Error("anyMerged = true, want false")
	}
	if !anyMerged([]subOutcome{{result: "blocked"}, {result: "merged"}}) {
		t.Error("anyMerged = false, want true")
	}
}

func TestBriefReason(t *testing.T) {
	if got := briefReason(&workerResult{Summary: "did it"}); got != "did it" {
		t.Errorf("got %q, want 'did it'", got)
	}
	if got := briefReason(&workerResult{Summary: "did it", Unresolved: []string{"x", "y"}}); got != "x; y" {
		t.Errorf("got %q, want 'x; y'", got)
	}
}

// guard: a freshly created project dir is recognised as non-git so the manager
// refuses rather than corrupting state.
func TestIsGitRepoOnPlainDir(t *testing.T) {
	dir := t.TempDir()
	if isGitRepo(dir) {
		t.Error("plain temp dir reported as git repo")
	}
	// sanity: a path that does not exist
	if isGitRepo(filepath.Join(dir, "nope")) {
		t.Error("missing dir reported as git repo")
	}
}
