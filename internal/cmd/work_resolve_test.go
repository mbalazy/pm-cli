package cmd

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// twoProjectStore builds two projects with no Path (so detectProjectFromCwd
// never matches and every lookup goes through the cross-project fallback).
func twoProjectStore(t *testing.T) *storage.Store {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	for _, slug := range []string{"alpha", "beta"} {
		if err := store.CreateProject(slug, &storage.Project{Name: slug}); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// TestResolveWorkTaskAmbiguousAcrossProjects: the one-arg form falls back to
// FindTask in EVERY project, and FindTask also matches on title SUBSTRING - so
// a short query can hit several projects. First-hit-wins in sorted
// ListProjects order picked one silently, which for `pm work` means spawning a
// 60-minute worker that commits in the WRONG repo.
func TestResolveWorkTaskAmbiguousAcrossProjects(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-1", Title: "Fix auth refresh", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "beta-1", Title: "Auth screen polish", Status: storage.StatusTodo}, "")

	_, _, err := resolveWorkTask(store, []string{"auth"}, "work")
	if err == nil {
		t.Fatal("ambiguous cross-project query resolved instead of erroring")
	}
	for _, want := range []string{"ambiguous", "alpha/alpha-1", "beta/beta-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q, got: %v", want, err)
		}
	}
}

// A query that matches exactly one project keeps resolving - the ambiguity
// guard must not cost the single-project case its convenience.
func TestResolveWorkTaskUniqueAcrossProjects(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-1", Title: "Fix auth refresh", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "beta-1", Title: "Unrelated", Status: storage.StatusTodo}, "")

	task, slug, err := resolveWorkTask(store, []string{"auth"}, "work")
	if err != nil {
		t.Fatalf("unique cross-project query: %v", err)
	}
	if slug != "alpha" || task.Meta.ID != "alpha-1" {
		t.Fatalf("resolved %s/%s, want alpha/alpha-1", slug, task.Meta.ID)
	}

	// And the explicit two-arg form still wins over everything.
	task, slug, err = resolveWorkTask(store, []string{"beta", "beta-1"}, "work")
	if err != nil {
		t.Fatalf("explicit project+task: %v", err)
	}
	if slug != "beta" || task.Meta.ID != "beta-1" {
		t.Fatalf("resolved %s/%s, want beta/beta-1", slug, task.Meta.ID)
	}
}

// The shared error text used to tell every caller to retry with `pm work`,
// including `pm run-epic`'s.
func TestResolveWorkTaskErrorNamesInvokingCommand(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-1", Title: "Fix auth refresh", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "beta-1", Title: "Auth screen polish", Status: storage.StatusTodo}, "")

	for _, tc := range []struct{ query, cmdName string }{
		{"nothing-matches-this", "run-epic"},
		{"auth", "run-epic"}, // ambiguous branch carries the name too
	} {
		_, _, err := resolveWorkTask(store, []string{tc.query}, tc.cmdName)
		if err == nil {
			t.Fatalf("query %q resolved unexpectedly", tc.query)
		}
		if !strings.Contains(err.Error(), "pm "+tc.cmdName+" <project> <task-id>") {
			t.Errorf("query %q: error must suggest `pm %s ...`, got: %v", tc.query, tc.cmdName, err)
		}
	}
}
