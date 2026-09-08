package cmd

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
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
// pm-cli-18 vs an atlas task mentioning it in its title.)
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

// TestResolveWorkTaskCwdIsATiebreakNotAnOverride: the cwd project used to
// short-circuit the whole scan - `FindTask` on it, first hit wins - so a TITLE
// mention in the repo you happen to be standing in beat another project's
// EXACT id, and the worker committed in the wrong repo. cwd must break ties
// WITHIN a tier, never outrank a stronger match.
func TestResolveWorkTaskCwdIsATiebreakNotAnOverride(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// "local" is the project whose path encloses the test's cwd, so
	// detectProjectFromCwd resolves to it for real.
	if err := store.CreateProject("local", &storage.Project{Name: "local", Path: cwd}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateProject("remote", &storage.Project{Name: "remote"}); err != nil {
		t.Fatal(err)
	}
	addTask(t, store, "local", storage.TaskMeta{ID: "local-1", Title: "Port remote-9 learnings", Status: storage.StatusTodo}, "")
	addTask(t, store, "remote", storage.TaskMeta{ID: "remote-9", Title: "The real one", Status: storage.StatusTodo}, "")

	task, slug, err := resolveWorkTask(store, []string{"remote-9"}, "work")
	if err != nil {
		t.Fatalf("exact id lost to a title mention in the cwd project: %v", err)
	}
	if slug != "remote" || task.Meta.ID != "remote-9" {
		t.Fatalf("resolved %s/%s, want remote/remote-9", slug, task.Meta.ID)
	}

	// Within ONE tier the cwd project does win - two equal title matches, and
	// standing in one of the repos settles it instead of erroring.
	addTask(t, store, "local", storage.TaskMeta{ID: "local-2", Title: "Sync widget", Status: storage.StatusTodo}, "")
	addTask(t, store, "remote", storage.TaskMeta{ID: "remote-2", Title: "Sync widget", Status: storage.StatusTodo}, "")
	task, slug, err = resolveWorkTask(store, []string{"sync widget"}, "work")
	if err != nil {
		t.Fatalf("cwd must settle a tie inside a tier: %v", err)
	}
	if slug != "local" || task.Meta.ID != "local-2" {
		t.Fatalf("resolved %s/%s, want local/local-2", slug, task.Meta.ID)
	}
}

// TestResolveWorkTaskIDPrefixBeatsTitleMention: FindTask's MIDDLE tier - a
// unique ID-prefix hit outranks a title substring - has to survive the
// cross-project scan too, or `pm work alpha-72` starts failing as ambiguous
// merely because another project has a task titled "port alpha-72 helper".
func TestResolveWorkTaskIDPrefixBeatsTitleMention(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-720", Title: "Real work", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "beta-3", Title: "Port alpha-72 helper", Status: storage.StatusTodo}, "")

	task, slug, err := resolveWorkTask(store, []string{"alpha-72"}, "work")
	if err != nil {
		t.Fatalf("unique id-prefix lost to a title mention: %v", err)
	}
	if slug != "alpha" || task.Meta.ID != "alpha-720" {
		t.Fatalf("resolved %s/%s, want alpha/alpha-720", slug, task.Meta.ID)
	}

	// Two projects each holding an id-prefix hit IS ambiguity.
	addTask(t, store, "beta", storage.TaskMeta{ID: "alpha-729", Title: "Stranger", Status: storage.StatusTodo}, "")
	if _, _, err := resolveWorkTask(store, []string{"alpha-72"}, "work"); err == nil {
		t.Fatal("an id-prefix hit in two projects must be ambiguous")
	}
}

// TestResolveWorkTaskSkipsArchivedProjects: a shelved project holding an
// imported ticket key must not make every query for that key ambiguous
// forever. GetAllTasks and pm context already scan active projects only.
func TestResolveWorkTaskSkipsArchivedProjects(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "ACME-253", Title: "Live work", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "ACME-253", Title: "Old client work", Status: storage.StatusTodo}, "")
	if _, err := store.MutateProject("beta", func(p *storage.Project) error {
		p.Archived = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	task, slug, err := resolveWorkTask(store, []string{"ACME-253"}, "work")
	if err != nil {
		t.Fatalf("an archived twin made a live task unreachable: %v", err)
	}
	if slug != "alpha" || task.Meta.ID != "ACME-253" {
		t.Fatalf("resolved %s/%s, want alpha/ACME-253", slug, task.Meta.ID)
	}

	// The explicit two-arg form still reaches the archived project.
	task, slug, err = resolveWorkTask(store, []string{"beta", "ACME-253"}, "work")
	if err != nil {
		t.Fatalf("explicit form must still reach an archived project: %v", err)
	}
	if slug != "beta" || task.Meta.Title != "Old client work" {
		t.Fatalf("resolved %s/%q, want beta/\"Old client work\"", slug, task.Meta.Title)
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

// unreadableProjectStore wraps a real TaskStore and makes every read on ONE
// slug fail with a synthetic error distinct from the "no such task" / "several
// matches" sentinels - simulating a project directory that cannot be read
// (permissions, a corrupt file) rather than one that is merely readable and
// empty of matches.
type unreadableProjectStore struct {
	storage.TaskStore
	slug string
	err  error
}

func (s *unreadableProjectStore) GetTasks(projectSlug string) ([]*storage.Task, error) {
	if projectSlug == s.slug {
		return nil, s.err
	}
	return s.TaskStore.GetTasks(projectSlug)
}

func (s *unreadableProjectStore) FindTask(projectSlug, query string) (*storage.Task, error) {
	if projectSlug == s.slug {
		return nil, s.err
	}
	return s.TaskStore.FindTask(projectSlug, query)
}

func (s *unreadableProjectStore) FindTaskExact(projectSlug, taskID string) (*storage.Task, error) {
	if projectSlug == s.slug {
		return nil, s.err
	}
	return s.TaskStore.FindTaskExact(projectSlug, taskID)
}

// TestResolveWorkTaskReportsUnreadableProject: a project whose directory
// cannot be read must not be silently dropped from the scan - the user gets a
// stderr note naming it, and the OTHER project's task still resolves.
func TestResolveWorkTaskReportsUnreadableProject(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-1", Title: "Fix auth refresh", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "beta-1", Title: "Unrelated", Status: storage.StatusTodo}, "")

	readErr := errors.New("open beta: permission denied")
	broken := &unreadableProjectStore{TaskStore: store, slug: "beta", err: readErr}

	var task *storage.Task
	var slug string
	var err error
	stderr := captureStderr(t, func() {
		task, slug, err = resolveWorkTask(broken, []string{"alpha-1"}, "work")
	})
	if err != nil {
		t.Fatalf("readable project's exact id must still resolve: %v", err)
	}
	if slug != "alpha" || task.Meta.ID != "alpha-1" {
		t.Fatalf("resolved %s/%s, want alpha/alpha-1", slug, task.Meta.ID)
	}
	if got := strings.Count(stderr, "skipping project beta"); got != 1 {
		t.Fatalf("want exactly one note about beta, got %d in: %q", got, stderr)
	}
	if !strings.Contains(stderr, readErr.Error()) {
		t.Fatalf("note must carry the underlying error, got: %q", stderr)
	}

	// A task that lives ONLY in the unreadable project stays not-found, but
	// the note still fires.
	stderr = captureStderr(t, func() {
		_, _, err = resolveWorkTask(broken, []string{"beta-1"}, "work")
	})
	if err == nil {
		t.Fatal("a task in an unreadable project must not resolve")
	}
	if !strings.Contains(stderr, "skipping project beta") {
		t.Fatalf("want a note about beta, got: %q", stderr)
	}
}

// TestResolveWorkTaskPlainMissPrintsNoNote: when every project is readable
// and the query simply matches nothing, no unreadable-project note should
// appear.
func TestResolveWorkTaskPlainMissPrintsNoNote(t *testing.T) {
	store := twoProjectStore(t)
	addTask(t, store, "alpha", storage.TaskMeta{ID: "alpha-1", Title: "Fix auth refresh", Status: storage.StatusTodo}, "")
	addTask(t, store, "beta", storage.TaskMeta{ID: "beta-1", Title: "Unrelated", Status: storage.StatusTodo}, "")

	var err error
	stderr := captureStderr(t, func() {
		_, _, err = resolveWorkTask(store, []string{"nothing-matches-this"}, "work")
	})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if stderr != "" {
		t.Fatalf("plain miss must print no note, got: %q", stderr)
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
