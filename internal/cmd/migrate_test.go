package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

func runMigrateIDsCmd(store storage.TaskStore) error {
	cmd := newMigrateIDsCmd(store)
	cmd.SetArgs(nil)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	return cmd.Execute()
}

// TestMigrateIDsRejectsUnsafePrefix: migrate-ids is the ONE renumbering path
// that does not go through AddTask - it rewrites existing files via the
// deliberately lenient WriteTask - so it has to validate the IDs it mints
// itself. With `prefix: ../x`, Filename+Join would drop the file outside the
// project dir, where ReadTasksFromDir never finds it again.
func TestMigrateIDsRejectsUnsafePrefix(t *testing.T) {
	root := t.TempDir()
	store := &storage.Store{Root: filepath.Join(root, "pm")}
	dir := store.ProjectDir("legacy")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Planted directly: CreateProject would reject this prefix now, but a
	// project.yaml hand-edited before the check exists is exactly the case.
	if err := storage.WriteProject(store.ProjectYAML("legacy"), &storage.Project{Name: "Legacy", Prefix: "../escaped"}); err != nil {
		t.Fatal(err)
	}
	task := &storage.Task{
		Meta:     storage.TaskMeta{ID: "old-1", Title: "Legacy task", Status: storage.StatusTodo},
		FilePath: filepath.Join(dir, "old-1-legacy-task.md"),
	}
	if err := storage.WriteTask(task); err != nil {
		t.Fatal(err)
	}

	err := runMigrateIDsCmd(store)
	if err == nil {
		t.Fatal("migrate-ids accepted a traversing prefix")
	}
	if !strings.Contains(err.Error(), "invalid task id") {
		t.Errorf("want a ValidateTaskID error, got: %v", err)
	}

	// The original task is untouched and nothing escaped the project dir.
	tasks, err := store.GetTasks("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Meta.ID != "old-1" {
		t.Fatalf("aborted migration must leave the task alone, got %+v", tasks)
	}
	for _, d := range []string{store.Root, root} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.Contains(e.Name(), "escaped") {
				t.Fatalf("migrate wrote outside the project dir: %s", filepath.Join(d, e.Name()))
			}
		}
	}
}

// The happy path still renumbers - the guard must not block a normal store.
func TestMigrateIDsRenumbersWithSafePrefix(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	if err := store.CreateProject("app", &storage.Project{Name: "App", Prefix: "ap"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"zz-9", "zz-4"} {
		addTask(t, store, "app", storage.TaskMeta{ID: id, Title: "T " + id, Status: storage.StatusTodo, Created: "2025-01-0" + id[3:]}, "")
	}

	if err := runMigrateIDsCmd(store); err != nil {
		t.Fatalf("migrate-ids: %v", err)
	}
	tasks, err := store.GetTasks("app")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tk := range tasks {
		got[tk.Meta.ID] = true
	}
	if !got["ap-1"] || !got["ap-2"] || len(tasks) != 2 {
		t.Fatalf("want ap-1 and ap-2, got %v", got)
	}
}
