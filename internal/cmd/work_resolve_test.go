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

// TestResolveWorkTaskExactIDBeatsTitleMention: FindTask ranks exact ID above
// title substring WITHIN a project, and the cross-project scan must keep that
// ranking. Flattening the tiers would make `pm work pm-cli-18` fail as
// "ambiguous" merely because another project has a task TITLED "smoke-test for
// pm-cli-18" - a weaker match, not a rival. (Real shape: this machine's
// pm-cli-18 vs a atlas task mentioning it in its title.)
func TestResolveWorkTaskExactIDBeatsTitleMention(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-18", Title: "Smoke-test epic for beta-18", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "beta-18", Title: "Executor", Status: storage.StatusTodo}, "")

	task, slug, err := resolveWorkTask(store, []string{"beta-18"}, "work")
	if err != nil {
		t.Fatalf("exact id lost to a title mention: %v", err)
	}
	if slug != "beta" || task.Meta.ID != "beta-18" {
		t.Fatalf("resolved %s/%s, want beta/beta-18", slug, task.Meta.ID)
	}

	// Two projects holding the SAME exact id is real ambiguity and must error.
	addTask(t, store, "alpha", storage.TaskMeta{ID: "shared-1", Title: "A", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "shared-1", Title: "B", Status: storage.StatusTodo}, "")
	if _, _, err := resolveWorkTask(store, []string{"shared-1"}, "work"); err == nil {
		t.Fatal("the same exact id in two projects must be ambiguous")
	}
}

// TestResolveWorkTaskIntraProjectAmbiguityCounts: FindTask returns an error
// for BOTH "no match here" and "several matches here". Collapsing them makes
// an ambiguous project contribute nothing, so the scan resolves to whichever
// OTHER project has a single hit - first-wins again, by another route, and
// again with a 60-minute worker committing in the wrong repo.
func TestResolveWorkTaskIntraProjectAmbiguityCounts(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-1", Title: "Auth refresh", Status: storage.StatusTodo}, "")
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-2", Title: "Auth screen", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "beta-1", Title: "Auth cleanup", Status: storage.StatusTodo}, "")

	_, _, err := resolveWorkTask(store, []string{"auth"}, "work")
	if err == nil {
		t.Fatal("a query ambiguous inside alpha resolved to beta instead of erroring")
	}
	for _, want := range []string{"ambiguous", "alpha/<several>", "beta/beta-1"} {
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
