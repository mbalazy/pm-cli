package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// newTestStore builds a store with one project ("test", prefix "t",
// statuses todo/doing/waiting/done) holding one task t-1 on doing.
func newTestStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	store := &storage.Store{Root: dir}
	projDir := filepath.Join(dir, "test")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteProject(filepath.Join(projDir, "project.yaml"), &storage.Project{
		Name:   "Test",
		Prefix: "t",
	}); err != nil {
		t.Fatal(err)
	}
	task := &storage.Task{
		Meta: storage.TaskMeta{
			ID:      "t-1",
			Title:   "Test Task",
			Status:  storage.StatusDoing,
			Created: "2025-01-01",
			Updated: "2025-01-01",
			Links:   map[string]string{"jira": "SCRUM-1"},
			Brief:   "initial brief",
			AC:      "- works",
		},
		Body:     "## Description\n\nOriginal body",
		FilePath: filepath.Join(projDir, "t-1-test-task.md"),
		Project:  "test",
	}
	if err := storage.WriteTask(task); err != nil {
		t.Fatal(err)
	}
	return store
}

func strp(s string) *string { return &s }

func wantValidation(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %T: %v", err, err)
	}
}

func taskFiles(t *testing.T, store *storage.Store) []string {
	t.Helper()
	entries, err := os.ReadDir(store.ProjectDir("test"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	return names
}

func TestAddTask(t *testing.T) {
	t.Run("defaults to the project's first status with aligned stamps", func(t *testing.T) {
		store := newTestStore(t)
		task, err := AddTask(store, AddTaskInput{Project: "test", Title: "Fresh"})
		if err != nil {
			t.Fatal(err)
		}
		if task.Meta.Status != storage.StatusTodo {
			t.Fatalf("status = %s, want todo", task.Meta.Status)
		}
		if task.Meta.ID != "t-2" {
			t.Fatalf("id = %s, want minted t-2", task.Meta.ID)
		}
		if task.Meta.Updated != task.Meta.StatusChanged {
			t.Fatalf("updated %q != status_changed %q on a new task", task.Meta.Updated, task.Meta.StatusChanged)
		}
		if _, err := store.FindTaskExact("test", "t-2"); err != nil {
			t.Fatalf("task not on disk: %v", err)
		}
	})

	t.Run("explicit status keeps updated == status_changed", func(t *testing.T) {
		store := newTestStore(t)
		task, err := AddTask(store, AddTaskInput{Project: "test", Title: "Now", Status: "doing"})
		if err != nil {
			t.Fatal(err)
		}
		if task.Meta.Status != storage.StatusDoing || task.Meta.Updated != task.Meta.StatusChanged {
			t.Fatalf("got status %s updated %q status_changed %q", task.Meta.Status, task.Meta.Updated, task.Meta.StatusChanged)
		}
	})

	t.Run("spec wraps the body, body becomes the Log", func(t *testing.T) {
		store := newTestStore(t)
		task, err := AddTask(store, AddTaskInput{Project: "test", Title: "S", Spec: "## Goal\nX", Body: "log line"})
		if err != nil {
			t.Fatal(err)
		}
		if storage.ExtractSpec(task.Body) != "## Goal\nX" {
			t.Fatalf("spec not applied: %q", task.Body)
		}
		if !strings.Contains(task.Body, "log line") {
			t.Fatalf("body lost: %q", task.Body)
		}
	})

	t.Run("child id minted under parent", func(t *testing.T) {
		store := newTestStore(t)
		task, err := AddTask(store, AddTaskInput{Project: "test", Title: "child", Parent: "t-1"})
		if err != nil {
			t.Fatal(err)
		}
		if task.Meta.ID != "t-1-1" || task.Meta.Parent != "t-1" {
			t.Fatalf("id %s parent %s", task.Meta.ID, task.Meta.Parent)
		}
	})

	t.Run("status outside the project's set is a ValidationError and writes nothing", func(t *testing.T) {
		store := newTestStore(t)
		before := taskFiles(t, store)
		_, err := AddTask(store, AddTaskInput{Project: "test", Title: "bad", Status: "shipped"})
		wantValidation(t, err)
		if !strings.Contains(err.Error(), `invalid status "shipped"`) {
			t.Fatalf("error = %v", err)
		}
		if after := taskFiles(t, store); len(after) != len(before) {
			t.Fatalf("a rejected add left files behind: %v", after)
		}
	})

	t.Run("invalid mode / epic_mode / finish_mode leave no phantom file", func(t *testing.T) {
		cases := []struct {
			name string
			in   AddTaskInput
		}{
			{"mode", AddTaskInput{Project: "test", Title: "m", Mode: "sometimes"}},
			{"epic_mode", AddTaskInput{Project: "test", Title: "e", EpicMode: "parallel"}},
			{"finish_mode", AddTaskInput{Project: "test", Title: "f", FinishMode: "maybe"}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				store := newTestStore(t)
				before := taskFiles(t, store)
				_, err := AddTask(store, c.in)
				wantValidation(t, err)
				if !strings.Contains(err.Error(), "invalid "+c.name) {
					t.Fatalf("error = %v", err)
				}
				if after := taskFiles(t, store); len(after) != len(before) {
					t.Fatalf("phantom file after rejected %s: %v", c.name, after)
				}
			})
		}
	})

	t.Run("unknown project is a plain error", func(t *testing.T) {
		store := newTestStore(t)
		_, err := AddTask(store, AddTaskInput{Project: "nope", Title: "x"})
		if err == nil {
			t.Fatal("expected error")
		}
		var ve *ValidationError
		if errors.As(err, &ve) {
			t.Fatalf("project resolution is not a caller validation failure: %v", err)
		}
	})
}

func TestUpdateTask(t *testing.T) {
	t.Run("tri-state: omit keeps, empty string clears, title never clears", func(t *testing.T) {
		store := newTestStore(t)
		if _, err := UpdateTask(store, UpdateTaskInput{
			Project: "test", TaskID: "t-1",
			Branch: strp("feat/x"), Parent: strp("t-p"), Brief: strp("b1"), AC: strp("a1"), WaitingFor: strp("review"),
		}); err != nil {
			t.Fatal(err)
		}
		task, _ := store.FindTaskExact("test", "t-1")
		if task.Meta.Branch != "feat/x" || task.Meta.Parent != "t-p" || task.Meta.Brief != "b1" || task.Meta.AC != "a1" || task.Meta.WaitingFor != "review" {
			t.Fatalf("set failed: %+v", task.Meta)
		}

		// omit everything but title: nothing else moves; empty title is a no-op
		if _, err := UpdateTask(store, UpdateTaskInput{Project: "test", TaskID: "t-1", Title: ""}); err != nil {
			t.Fatal(err)
		}
		task, _ = store.FindTaskExact("test", "t-1")
		if task.Meta.Title != "Test Task" || task.Meta.Branch != "feat/x" || task.Meta.Brief != "b1" || task.Meta.WaitingFor != "review" {
			t.Fatalf("omitted fields moved: %+v", task.Meta)
		}

		if _, err := UpdateTask(store, UpdateTaskInput{
			Project: "test", TaskID: "t-1",
			Branch: strp(""), Parent: strp(""), Brief: strp(""), AC: strp(""), WaitingFor: strp(""),
		}); err != nil {
			t.Fatal(err)
		}
		task, _ = store.FindTaskExact("test", "t-1")
		if task.Meta.Branch != "" || task.Meta.Parent != "" || task.Meta.Brief != "" || task.Meta.AC != "" || task.Meta.WaitingFor != "" {
			t.Fatalf("empty string must clear: %+v", task.Meta)
		}
	})

	t.Run("links merge, tags replace, sessions append, Spec rewrites, Log appends", func(t *testing.T) {
		store := newTestStore(t)
		if _, err := UpdateTask(store, UpdateTaskInput{
			Project: "test", TaskID: "t-1",
			Links: map[string]string{"pr": "https://x/1"}, Tags: []string{"a"}, Sessions: []string{"s1"},
			Spec: "## Goal\nv1", BodyAppend: "Session 1",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := UpdateTask(store, UpdateTaskInput{
			Project: "test", TaskID: "t-1",
			Tags: []string{"b"}, Sessions: []string{"s2"}, Spec: "## Goal\nv2", BodyAppend: "Session 2",
		}); err != nil {
			t.Fatal(err)
		}
		task, _ := store.FindTaskExact("test", "t-1")
		if task.Meta.Links["jira"] != "SCRUM-1" || task.Meta.Links["pr"] != "https://x/1" {
			t.Fatalf("links must merge: %v", task.Meta.Links)
		}
		if len(task.Meta.Tags) != 1 || task.Meta.Tags[0] != "b" {
			t.Fatalf("tags must replace: %v", task.Meta.Tags)
		}
		if strings.Join(task.Meta.Sessions, ",") != "s1,s2" {
			t.Fatalf("sessions must append: %v", task.Meta.Sessions)
		}
		if storage.ExtractSpec(task.Body) != "## Goal\nv2" || strings.Contains(task.Body, "v1") {
			t.Fatalf("spec must be rewritten in place: %q", task.Body)
		}
		for _, want := range []string{"Original body", "Session 1", "Session 2"} {
			if !strings.Contains(task.Body, want) {
				t.Fatalf("log lost %q: %q", want, task.Body)
			}
		}
	})

	t.Run("status change is validated and stamped", func(t *testing.T) {
		store := newTestStore(t)
		_, err := UpdateTask(store, UpdateTaskInput{Project: "test", TaskID: "t-1", Status: "shipped"})
		wantValidation(t, err)
		task, _ := store.FindTaskExact("test", "t-1")
		if task.Meta.Status != storage.StatusDoing {
			t.Fatalf("rejected status was written: %s", task.Meta.Status)
		}

		if _, err := UpdateTask(store, UpdateTaskInput{Project: "test", TaskID: "t-1", Status: "waiting"}); err != nil {
			t.Fatal(err)
		}
		task, _ = store.FindTaskExact("test", "t-1")
		if task.Meta.Status != storage.StatusWaiting || task.Meta.StatusChanged == "" {
			t.Fatalf("status %s status_changed %q", task.Meta.Status, task.Meta.StatusChanged)
		}

		// archived is system-level: always legal
		if _, err := UpdateTask(store, UpdateTaskInput{Project: "test", TaskID: "t-1", Status: "archived"}); err != nil {
			t.Fatalf("archived must be legal: %v", err)
		}
	})

	t.Run("invalid mode / epic_mode / finish_mode are ValidationErrors", func(t *testing.T) {
		store := newTestStore(t)
		for _, in := range []UpdateTaskInput{
			{Project: "test", TaskID: "t-1", Mode: strp("later")},
			{Project: "test", TaskID: "t-1", EpicMode: strp("parallel")},
			{Project: "test", TaskID: "t-1", FinishMode: strp("maybe")},
		} {
			_, err := UpdateTask(store, in)
			wantValidation(t, err)
		}
	})

	t.Run("fuzzy task id resolves", func(t *testing.T) {
		store := newTestStore(t)
		task, err := UpdateTask(store, UpdateTaskInput{Project: "test", TaskID: "Test Task", Title: "renamed"})
		if err != nil {
			t.Fatal(err)
		}
		if task.Meta.ID != "t-1" || task.Meta.Title != "renamed" {
			t.Fatalf("got %s %q", task.Meta.ID, task.Meta.Title)
		}
	})
}

func TestMoveTask(t *testing.T) {
	t.Run("moves and reports old/new", func(t *testing.T) {
		store := newTestStore(t)
		res, err := MoveTask(store, MoveTaskInput{Project: "test", TaskID: "t-1", NewStatus: "done"})
		if err != nil {
			t.Fatal(err)
		}
		if res.ID != "t-1" || res.OldStatus != "doing" || res.NewStatus != "done" {
			t.Fatalf("result %+v", res)
		}
		task, _ := store.FindTaskExact("test", "t-1")
		if task.Meta.Status != storage.StatusDone || task.Meta.Brief != "initial brief" {
			t.Fatalf("move must change status only: %+v", task.Meta)
		}
	})

	t.Run("status outside the set is a ValidationError, nothing written", func(t *testing.T) {
		store := newTestStore(t)
		_, err := MoveTask(store, MoveTaskInput{Project: "test", TaskID: "t-1", NewStatus: "shipped"})
		wantValidation(t, err)
		task, _ := store.FindTaskExact("test", "t-1")
		if task.Meta.Status != storage.StatusDoing {
			t.Fatalf("status = %s", task.Meta.Status)
		}
	})

	t.Run("archived is always legal", func(t *testing.T) {
		store := newTestStore(t)
		if _, err := MoveTask(store, MoveTaskInput{Project: "test", TaskID: "t-1", NewStatus: "archived"}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown task is a plain error", func(t *testing.T) {
		store := newTestStore(t)
		_, err := MoveTask(store, MoveTaskInput{Project: "test", TaskID: "t-99", NewStatus: "done"})
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestDeleteTask(t *testing.T) {
	t.Run("fuzzy query deletes nothing", func(t *testing.T) {
		store := newTestStore(t)
		if _, err := DeleteTask(store, DeleteTaskInput{Project: "test", TaskID: "Test Task"}); err == nil {
			t.Fatal("title query must error")
		}
		if _, err := DeleteTask(store, DeleteTaskInput{Project: "test", TaskID: "t"}); err == nil {
			t.Fatal("prefix query must error")
		}
		if _, err := store.FindTaskExact("test", "t-1"); err != nil {
			t.Fatalf("task must survive: %v", err)
		}
	})

	t.Run("exact id removes the file", func(t *testing.T) {
		store := newTestStore(t)
		res, err := DeleteTask(store, DeleteTaskInput{Project: "test", TaskID: "t-1"})
		if err != nil {
			t.Fatal(err)
		}
		if res.ID != "t-1" || res.Title != "Test Task" {
			t.Fatalf("result %+v", res)
		}
		if _, err := store.FindTaskExact("test", "t-1"); err == nil {
			t.Fatal("task still exists")
		}
	})
}
