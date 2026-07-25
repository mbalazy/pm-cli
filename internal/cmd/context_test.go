package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func TestStatusMark(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   string
	}{
		{"done", "done", "✅"},
		{"merged", "merged", "🔀"},
		{"doing", "doing", "🔨"},
		{"waiting", "waiting", "⏳"},
		{"todo falls through to the default", "todo", "○"},
		{"unknown project status falls through", "review", "○"},
		{"empty falls through", "", "○"},
		{"marks are case-sensitive", "Done", "○"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusMark(tc.status); got != tc.want {
				t.Errorf("statusMark(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

func TestProgressLine(t *testing.T) {
	cases := []struct {
		name    string
		tracker storage.Tracker
		want    string
	}{
		{
			name:    "no children",
			tracker: storage.Tracker{Total: 0},
			want:    "0 total: ",
		},
		{
			name:    "single status",
			tracker: storage.Tracker{Total: 1, Progress: map[string]int{"todo": 1}},
			want:    "1 total: 1 todo",
		},
		{
			// keys are listed alphabetically, not in map order
			name:    "several statuses sort alphabetically",
			tracker: storage.Tracker{Total: 6, Progress: map[string]int{"todo": 2, "done": 1, "merged": 3}},
			want:    "6 total: 1 done, 3 merged, 2 todo",
		},
		{
			// Total comes off the tracker, it is not re-derived from Progress
			name:    "total is independent of the progress sum",
			tracker: storage.Tracker{Total: 9, Progress: map[string]int{"doing": 1}},
			want:    "9 total: 1 doing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := progressLine(tc.tracker); got != tc.want {
				t.Errorf("progressLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCountsLine(t *testing.T) {
	cases := []struct {
		name   string
		counts map[string]int
		want   string
	}{
		{"nil map", nil, ""},
		{"empty map", map[string]int{}, ""},
		{"single status", map[string]int{"doing": 3}, "3 doing"},
		{
			name:   "several statuses sort alphabetically",
			counts: map[string]int{"todo": 5, "doing": 2, "done": 1},
			want:   "2 doing, 1 done, 5 todo",
		},
		{
			name:   "archived is rendered like any other key",
			counts: map[string]int{"archived": 4, "todo": 1},
			want:   "4 archived, 1 todo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countsLine(tc.counts); got != tc.want {
				t.Errorf("countsLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

// contextFixture builds a store in a temp dir holding one project ("app") with
// the given tasks. The project has no Path, so detectProjectFromCwd can never
// match it - context always has to be reached via the explicit project arg.
func contextFixture(t *testing.T, tasks ...*storage.Task) *storage.Store {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	proj := &storage.Project{
		Name:     "App",
		Statuses: []string{"todo", "doing", "waiting", "merged", "done"},
	}
	if err := store.CreateProject("app", proj); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if err := store.AddTask("app", task); err != nil {
			t.Fatalf("AddTask %s: %v", task.Meta.ID, err)
		}
	}
	return store
}

// execContextCmd executes `pm context <args...>` against the store and returns
// what the command printed to stdout plus its error.
func execContextCmd(t *testing.T, store storage.TaskStore, args ...string) (string, error) {
	t.Helper()
	// A nil slice makes cobra fall back to os.Args[1:], which would leak the
	// test binary's own positionals into `pm context`.
	if args == nil {
		args = []string{}
	}
	var err error
	out := captureStdout(t, func() {
		cmd := newContextCmd(store)
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		err = cmd.Execute()
	})
	return out, err
}

// TestContextCmdTrackerRollup covers the rollup path: a tracker renders with a
// progress line and its children sorted by Order, children and the tracker
// itself are suppressed from the flat doing list, and counts cover every task.
func TestContextCmdTrackerRollup(t *testing.T) {
	store := contextFixture(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "Tracker epic", Status: storage.StatusDoing}},
		&storage.Task{Meta: storage.TaskMeta{ID: "app-2", Title: "Second sub", Status: storage.StatusTodo, Parent: "app-1", Order: 10}},
		&storage.Task{Meta: storage.TaskMeta{ID: "app-3", Title: "Third sub", Status: storage.TaskStatus("merged"), Parent: "app-1", Order: 20,
			Branch: "feat/three", Brief: "**Bold** first line\nsecond line"}},
		&storage.Task{Meta: storage.TaskMeta{ID: "app-4", Title: "First sub", Status: storage.StatusDoing, Parent: "app-1", Order: 5, Branch: "feat/four"}},
		&storage.Task{Meta: storage.TaskMeta{ID: "app-9", Title: "Standalone doing", Status: storage.StatusDoing}},
		&storage.Task{Meta: storage.TaskMeta{ID: "app-10", Title: "Standalone todo", Status: storage.StatusTodo}},
	)

	out, err := execContextCmd(t, store, "app")
	if err != nil {
		t.Fatalf("context: %v", err)
	}

	if !strings.HasPrefix(out, "# app\n\n") {
		t.Errorf("expected output to start with the project heading, got:\n%s", out)
	}
	mustContain(t, out, "📋 app-1  [doing]  (3 total: 1 doing, 1 merged, 1 todo)")
	mustContain(t, out, "   Tracker epic\n")

	// Child lines: mark, padded ID/status/order, branch appended only when set.
	mustContain(t, out, "   🔨 app-4         doing   o:5     feat/four\n")
	mustContain(t, out, "   ○ app-2         todo    o:10  \n")
	mustContain(t, out, "   🔀 app-3         merged  o:20    feat/three\n")

	// Only a child with a brief gets the continuation line, bold-stripped and
	// reduced to the first line.
	mustContain(t, out, "        └ Bold first line\n")
	if strings.Contains(out, "second line") {
		t.Errorf("brief line should stop at the first line, got:\n%s", out)
	}
	if n := strings.Count(out, "└"); n != 1 {
		t.Errorf("expected exactly 1 brief line, got %d\n%s", n, out)
	}

	// The tracker and its children are suppressed from the flat doing list.
	// Needles carry the two-space ID/title separator so "app-1" cannot match
	// the fixture's own app-10.
	mustContain(t, out, "## Doing (standalone)\n  • app-9  Standalone doing\n")
	if strings.Contains(out, "• app-1  ") || strings.Contains(out, "• app-4  ") {
		t.Errorf("tracker/child leaked into the standalone doing list:\n%s", out)
	}

	// Counts cover every task, tracker and children included.
	mustContain(t, out, "## Counts: 3 doing, 1 merged, 2 todo\n")

	// Children render in Order (5, 10, 20), NOT in ID or insertion order -
	// substring assertions alone are position-blind, so pin the whole output.
	// This also locks the blank-line separators between the sections.
	want := strings.Join([]string{
		"# app",
		"",
		"📋 app-1  [doing]  (3 total: 1 doing, 1 merged, 1 todo)",
		"   Tracker epic",
		"   🔨 app-4         doing   o:5     feat/four",
		"   ○ app-2         todo    o:10  ",
		"   🔀 app-3         merged  o:20    feat/three",
		"        └ Bold first line",
		"",
		"## Doing (standalone)",
		"  • app-9  Standalone doing",
		"",
		"## Counts: 3 doing, 1 merged, 2 todo",
		"",
	}, "\n")
	if out != want {
		t.Errorf("rollup output mismatch\n--- got ---\n%q\n--- want ---\n%q", out, want)
	}
}

// TestContextCmdNoTrackers covers the flat path: no parent anywhere means no
// rollup block at all, and every doing task shows up standalone.
func TestContextCmdNoTrackers(t *testing.T) {
	store := contextFixture(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "Flat doing", Status: storage.StatusDoing}},
		&storage.Task{Meta: storage.TaskMeta{ID: "app-2", Title: "Flat todo", Status: storage.StatusTodo}},
	)

	out, err := execContextCmd(t, store, "app")
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if strings.Contains(out, "📋") {
		t.Errorf("expected no tracker block, got:\n%s", out)
	}
	mustContain(t, out, "## Doing (standalone)\n  • app-1  Flat doing\n")
	mustContain(t, out, "## Counts: 1 doing, 1 todo\n")
}

// TestContextCmdEmptyProject: a project with no tasks still prints a heading
// and an (empty) counts line, and skips both optional sections.
func TestContextCmdEmptyProject(t *testing.T) {
	store := contextFixture(t)

	out, err := execContextCmd(t, store, "app")
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if out != "# app\n\n## Counts: \n" {
		t.Errorf("unexpected empty-project output: %q", out)
	}
}

// TestContextCmdNoDoing: tasks exist but none is doing, so the standalone
// section is skipped while the tracker block still renders.
func TestContextCmdNoDoing(t *testing.T) {
	store := contextFixture(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "Tracker", Status: storage.StatusWaiting}},
		&storage.Task{Meta: storage.TaskMeta{ID: "app-2", Title: "Sub", Status: storage.StatusDone, Parent: "app-1"}},
	)

	out, err := execContextCmd(t, store, "app")
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	mustContain(t, out, "📋 app-1  [waiting]  (1 total: 1 done)")
	mustContain(t, out, "   ✅ app-2         done    o:0   \n")
	if strings.Contains(out, "## Doing (standalone)") {
		t.Errorf("expected no standalone doing section, got:\n%s", out)
	}
	mustContain(t, out, "## Counts: 1 done, 1 waiting\n")
}

func TestContextCmdErrors(t *testing.T) {
	store := contextFixture(t,
		&storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "T", Status: storage.StatusDoing}},
	)

	t.Run("unknown project", func(t *testing.T) {
		out, err := execContextCmd(t, store, "nosuch")
		if err == nil {
			t.Fatalf("expected an error for an unknown project, got output:\n%s", out)
		}
		if !strings.Contains(err.Error(), "project not found") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("no arg and cwd matches no project", func(t *testing.T) {
		// The fixture project has no Path, so cwd detection cannot match it.
		out, err := execContextCmd(t, store)
		if err == nil {
			t.Fatalf("expected an error without a project, got output:\n%s", out)
		}
		if !strings.Contains(err.Error(), "no project") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("more than one arg is rejected", func(t *testing.T) {
		if _, err := execContextCmd(t, store, "app", "extra"); err == nil {
			t.Fatal("expected an error for two args")
		}
	})
}
