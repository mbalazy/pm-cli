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

func TestRecordSubFeedback(t *testing.T) {
	store, slug := tempStore(t)

	t.Run("appends Manager Notes section into the spec", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-10", Title: "Epic", Status: storage.StatusDoing},
			storage.SpecStart+"\n## Description\nbuild it\n"+storage.SpecEnd+"\n\nlog tail")

		if err := recordSubFeedback(store, parent, "proj-10-1", "merged", []string{"streak store seam"}); err != nil {
			t.Fatalf("recordSubFeedback: %v", err)
		}
		// second finding from a later sub accumulates
		if err := recordSubFeedback(store, parent, "proj-10-2", "merged", []string{"nav handoff"}); err != nil {
			t.Fatalf("recordSubFeedback 2: %v", err)
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

	t.Run("tags a parked sub and refreshes it in place on re-run", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-12", Title: "Epic3", Status: storage.StatusDoing},
			storage.SpecStart+"\n## Description\nbuild it\n"+storage.SpecEnd)

		if err := recordSubFeedback(store, parent, "proj-12-2", "blocked", []string{"needs API field X"}); err != nil {
			t.Fatalf("record blocked: %v", err)
		}
		reloaded, _ := store.FindTask(slug, "proj-12")
		spec := storage.ExtractSpec(reloaded.Body)
		mustContain(t, spec, "[proj-12-2 · blocked] needs API field X")

		// Re-run: same sub, fresh reason -> the old line is replaced, not stacked.
		if err := recordSubFeedback(store, reloaded, "proj-12-2", "blocked", []string{"still needs API field X (v2)"}); err != nil {
			t.Fatalf("record blocked 2: %v", err)
		}
		reloaded2, _ := store.FindTask(slug, "proj-12")
		spec2 := storage.ExtractSpec(reloaded2.Body)
		mustContain(t, spec2, "[proj-12-2 · blocked] still needs API field X (v2)")
		if strings.Contains(spec2, "needs API field X\n") || strings.Count(spec2, "proj-12-2") != 1 {
			t.Errorf("expected exactly one proj-12-2 line after re-run, got:\n%s", spec2)
		}
	})

	t.Run("a shared-prefix sub id is not clobbered", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-13", Title: "Epic4", Status: storage.StatusDoing},
			storage.SpecStart+"\n## Description\nx\n"+storage.SpecEnd)
		_ = recordSubFeedback(store, parent, "proj-13-1", "blocked", []string{"one"})
		reloaded, _ := store.FindTask(slug, "proj-13")
		_ = recordSubFeedback(store, reloaded, "proj-13-12", "blocked", []string{"twelve"})
		// refreshing proj-13-1 must leave proj-13-12 intact
		reloaded2, _ := store.FindTask(slug, "proj-13")
		_ = recordSubFeedback(store, reloaded2, "proj-13-1", "blocked", []string{"one-again"})
		final, _ := store.FindTask(slug, "proj-13")
		spec := storage.ExtractSpec(final.Body)
		mustContain(t, spec, "[proj-13-1 · blocked] one-again")
		mustContain(t, spec, "[proj-13-12 · blocked] twelve")
	})

	t.Run("falls back to Log when parent has no spec", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-11", Title: "Epic2", Status: storage.StatusDoing}, "plain body, no markers")
		if err := recordSubFeedback(store, parent, "proj-11-1", "merged", []string{"finding x"}); err != nil {
			t.Fatalf("recordSubFeedback: %v", err)
		}
		reloaded, _ := store.FindTask(slug, "proj-11")
		if storage.ExtractSpec(reloaded.Body) != "" {
			t.Error("did not expect a spec block to be created")
		}
		mustContain(t, reloaded.Body, "[proj-11-1] finding x")
		mustContain(t, reloaded.Body, "plain body, no markers")
	})
}

func TestParkedFindings(t *testing.T) {
	if got := parkedFindings(&workerResult{Unresolved: []string{"q1", "q2"}}); strings.Join(got, ",") != "q1,q2" {
		t.Errorf("unresolved should win: %v", got)
	}
	if got := parkedFindings(&workerResult{Summary: "tests red"}); strings.Join(got, ",") != "tests red" {
		t.Errorf("summary fallback: %v", got)
	}
	if got := parkedFindings(&workerResult{}); strings.Join(got, ",") != "(no detail)" {
		t.Errorf("empty fallback: %v", got)
	}
}

func TestManagerNoteBlock(t *testing.T) {
	if got := managerNoteBlock("s-1", "merged", []string{"a", "b"}); got != "- [s-1] a\n- [s-1] b" {
		t.Errorf("merged tag: got %q", got)
	}
	if got := managerNoteBlock("s-1", "blocked", []string{"a"}); got != "- [s-1 · blocked] a" {
		t.Errorf("blocked tag: got %q", got)
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
