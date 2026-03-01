package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// setupMCPTestStore creates a temp store with a project and a task.
func setupMCPTestStore(t *testing.T) (*storage.Store, *storage.Task) {
	t.Helper()
	dir := t.TempDir()
	store := &storage.Store{Root: dir}

	projDir := filepath.Join(dir, "test")
	os.MkdirAll(projDir, 0755)
	storage.WriteProject(filepath.Join(projDir, "project.yaml"), &storage.Project{
		Name:   "Test Project",
		Prefix: "t",
		Path:   "/home/user/test",
	})

	task := &storage.Task{
		Meta: storage.TaskMeta{
			ID:      "t-1",
			Title:   "Test Task",
			Status:  storage.StatusDoing,
			Created: "2025-01-01",
			Updated: "2025-01-01",
			Links:   map[string]string{"jira": "SCRUM-1"},
			Brief:   "initial brief",
		},
		Body:     "## Description\n\nOriginal body",
		FilePath: filepath.Join(projDir, "t-1-test-task.md"),
		Project:  "test",
	}
	storage.WriteTask(task)

	return store, task
}

func TestUpdateTaskLinksOnlyMerge(t *testing.T) {
	t.Run("new key added", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		// Simulate MCP update: merge new link
		if task.Meta.Links == nil {
			task.Meta.Links = make(map[string]string)
		}
		task.Meta.Links["pr"] = "https://github.com/pr/1"
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Links["jira"] != "SCRUM-1" {
			t.Error("existing link 'jira' was removed")
		}
		if reloaded.Meta.Links["pr"] != "https://github.com/pr/1" {
			t.Error("new link 'pr' was not added")
		}
	})

	t.Run("existing key updated", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Links["jira"] = "SCRUM-2"
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Links["jira"] != "SCRUM-2" {
			t.Errorf("link not updated, got %q", reloaded.Meta.Links["jira"])
		}
	})

	t.Run("empty links input preserves existing", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		// Simulate: no links in update input (don't touch task.Meta.Links)
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Links["jira"] != "SCRUM-1" {
			t.Error("existing link lost when no links provided")
		}
	})

	t.Run("merge from nil links", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		// Simulate task with nil links getting a link
		task.Meta.Links = nil
		storage.WriteTask(task)
		task, _ = store.FindTask("test", "t-1")

		if task.Meta.Links == nil {
			task.Meta.Links = make(map[string]string)
		}
		task.Meta.Links["pr"] = "https://github.com/pr/1"
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Links["pr"] != "https://github.com/pr/1" {
			t.Error("link not added to previously nil links")
		}
	})
}

func TestUpdateTaskBodyOnlyAppends(t *testing.T) {
	t.Run("append to existing body", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		// Simulate MCP body_append logic
		appendText := "## Notes\n\nNew note"
		if task.Body != "" {
			task.Body = task.Body + "\n\n" + appendText
		} else {
			task.Body = appendText
		}
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Body != "## Description\n\nOriginal body\n\n## Notes\n\nNew note" {
			t.Errorf("body not appended correctly, got:\n%s", reloaded.Body)
		}
	})

	t.Run("append to empty body", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")
		task.Body = ""
		storage.WriteTask(task)

		task, _ = store.FindTask("test", "t-1")
		appendText := "First content"
		if task.Body != "" {
			task.Body = task.Body + "\n\n" + appendText
		} else {
			task.Body = appendText
		}
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Body != "First content" {
			t.Errorf("body = %q, want %q", reloaded.Body, "First content")
		}
	})

	t.Run("empty append preserves body", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		// Simulate: no body_append in update (don't touch task.Body)
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Body != "## Description\n\nOriginal body" {
			t.Errorf("body changed unexpectedly: %q", reloaded.Body)
		}
	})
}

func TestUpdateTaskBriefOverwrites(t *testing.T) {
	t.Run("brief overwritten", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Brief = "new brief"
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Brief != "new brief" {
			t.Errorf("brief = %q, want %q", reloaded.Meta.Brief, "new brief")
		}
	})

	t.Run("empty brief input preserves existing", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		// Simulate: no brief in update (don't touch task.Meta.Brief)
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Brief != "initial brief" {
			t.Errorf("brief changed unexpectedly: %q", reloaded.Meta.Brief)
		}
	})
}

func TestMoveTaskClearsBriefOnDoneArchived(t *testing.T) {
	t.Run("done clears brief", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		// Simulate pm_move_task handler logic
		task.Meta.Status = storage.StatusDone
		if task.Meta.Status == storage.StatusDone || task.Meta.Status == storage.StatusArchived {
			task.Meta.Brief = ""
		}
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Brief != "" {
			t.Errorf("brief should be cleared on done, got %q", reloaded.Meta.Brief)
		}
	})

	t.Run("archived clears brief", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Status = storage.StatusArchived
		if task.Meta.Status == storage.StatusDone || task.Meta.Status == storage.StatusArchived {
			task.Meta.Brief = ""
		}
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Brief != "" {
			t.Errorf("brief should be cleared on archived, got %q", reloaded.Meta.Brief)
		}
	})

	t.Run("move to other status preserves brief", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Status = storage.StatusWaiting
		if task.Meta.Status == storage.StatusDone || task.Meta.Status == storage.StatusArchived {
			task.Meta.Brief = ""
		}
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Brief != "initial brief" {
			t.Errorf("brief should be preserved on waiting, got %q", reloaded.Meta.Brief)
		}
	})
}

func TestResolveProjectFromCwd(t *testing.T) {
	dir := t.TempDir()
	store := &storage.Store{Root: dir}

	projDir := filepath.Join(dir, "myproject")
	os.MkdirAll(projDir, 0755)
	storage.WriteProject(filepath.Join(projDir, "project.yaml"), &storage.Project{
		Name: "My Project",
		Path: "/home/user/code/myproject",
	})

	t.Run("exact path match", func(t *testing.T) {
		slug, err := resolveProjectFromCwd(store, "/home/user/code/myproject")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slug != "myproject" {
			t.Errorf("got %q, want %q", slug, "myproject")
		}
	})

	t.Run("subdirectory match", func(t *testing.T) {
		slug, err := resolveProjectFromCwd(store, "/home/user/code/myproject/src/components")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slug != "myproject" {
			t.Errorf("got %q, want %q", slug, "myproject")
		}
	})

	t.Run("no match", func(t *testing.T) {
		_, err := resolveProjectFromCwd(store, "/somewhere/else")
		if err == nil {
			t.Error("expected error for unmatched cwd")
		}
	})

	t.Run("project without path skipped", func(t *testing.T) {
		noPathDir := filepath.Join(dir, "nopath")
		os.MkdirAll(noPathDir, 0755)
		storage.WriteProject(filepath.Join(noPathDir, "project.yaml"), &storage.Project{
			Name: "No Path",
		})

		_, err := resolveProjectFromCwd(store, "/anywhere")
		if err == nil {
			t.Error("expected error - project without path should not match")
		}
	})
}
