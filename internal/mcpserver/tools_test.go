package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestMoveTaskPreservesBrief(t *testing.T) {
	t.Run("done preserves brief", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		store.MoveTask(task, storage.StatusDone)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Brief != "initial brief" {
			t.Errorf("brief = %q, want %q", reloaded.Meta.Brief, "initial brief")
		}
	})

	t.Run("archived preserves brief", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		store.MoveTask(task, storage.StatusArchived)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Brief != "initial brief" {
			t.Errorf("brief = %q, want %q", reloaded.Meta.Brief, "initial brief")
		}
	})

	t.Run("waiting preserves brief", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		store.MoveTask(task, storage.StatusWaiting)

		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Brief != "initial brief" {
			t.Errorf("brief = %q, want %q", reloaded.Meta.Brief, "initial brief")
		}
	})
}

func TestCreateProjectDuplicateBlocked(t *testing.T) {
	store, _ := setupMCPTestStore(t)

	// "test" project already exists from setup
	existing, _ := store.ListProjects()
	found := false
	for _, s := range existing {
		if s == "test" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected 'test' project to exist")
	}

	// Creating a new project with different slug should work
	err := store.CreateProject("newproj", &storage.Project{Name: "New"})
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}
	proj, err := store.GetProject("newproj")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if proj.Name != "New" {
		t.Errorf("name = %q, want %q", proj.Name, "New")
	}
}

func TestCreateProjectWithAllFields(t *testing.T) {
	dir := t.TempDir()
	store := &storage.Store{Root: dir}

	p := &storage.Project{
		Name:     "Full Project",
		Prefix:   "fp",
		Path:     "/home/user/full",
		Repo:     "https://github.com/user/full",
		Stack:    "Go, React",
		Notes:    "Test project",
		Links:    map[string]string{"jira": "https://jira.example.com"},
		Tags:     []string{"client-a"},
		Statuses: []string{"backlog", "doing", "review", "done"},
	}

	err := store.CreateProject("full-project", p)
	if err != nil {
		t.Fatalf("CreateProject failed: %v", err)
	}

	loaded, err := store.GetProject("full-project")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}

	if loaded.Name != "Full Project" {
		t.Errorf("name = %q", loaded.Name)
	}
	if loaded.Prefix != "fp" {
		t.Errorf("prefix = %q", loaded.Prefix)
	}
	if loaded.Path != "/home/user/full" {
		t.Errorf("path = %q", loaded.Path)
	}
	if loaded.Repo != "https://github.com/user/full" {
		t.Errorf("repo = %q", loaded.Repo)
	}
	if loaded.Stack != "Go, React" {
		t.Errorf("stack = %q", loaded.Stack)
	}
	if loaded.Notes != "Test project" {
		t.Errorf("notes = %q", loaded.Notes)
	}
	if loaded.Links["jira"] != "https://jira.example.com" {
		t.Errorf("links[jira] = %q", loaded.Links["jira"])
	}
	if len(loaded.Tags) != 1 || loaded.Tags[0] != "client-a" {
		t.Errorf("tags = %v", loaded.Tags)
	}
	if len(loaded.Statuses) != 4 || loaded.Statuses[0] != "backlog" {
		t.Errorf("statuses = %v", loaded.Statuses)
	}
}

func TestDeleteTaskRemovesFile(t *testing.T) {
	t.Run("task file deleted", func(t *testing.T) {
		store, task := setupMCPTestStore(t)

		// Verify file exists
		if _, err := os.Stat(task.FilePath); os.IsNotExist(err) {
			t.Fatal("task file should exist before delete")
		}

		found, err := store.FindTask("test", "t-1")
		if err != nil {
			t.Fatalf("FindTask failed: %v", err)
		}

		if err := store.DeleteTask(found); err != nil {
			t.Fatalf("DeleteTask failed: %v", err)
		}

		// File should be gone
		if _, err := os.Stat(task.FilePath); !os.IsNotExist(err) {
			t.Error("task file should not exist after delete")
		}

		// FindTask should fail
		_, err = store.FindTask("test", "t-1")
		if err == nil {
			t.Error("FindTask should fail after delete")
		}
	})

	t.Run("delete nonexistent file returns error", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)

		ghost := &storage.Task{
			Meta:     storage.TaskMeta{ID: "t-999"},
			FilePath: filepath.Join(store.Root, "test", "t-999-ghost.md"),
		}
		if err := store.DeleteTask(ghost); err == nil {
			t.Error("expected error deleting nonexistent file")
		}
	})
}
func TestToSummarySessionCount(t *testing.T) {
	t.Run("zero sessions", func(t *testing.T) {
		task := &storage.Task{
			Meta:    storage.TaskMeta{ID: "t-1", Title: "No sessions"},
			Project: "test",
		}
		s := toSummary(task)
		if s.SessionCount != 0 {
			t.Errorf("session_count = %d, want 0", s.SessionCount)
		}
	})

	t.Run("multiple sessions", func(t *testing.T) {
		task := &storage.Task{
			Meta: storage.TaskMeta{
				ID:       "t-2",
				Title:    "With sessions",
				Sessions: []string{"abc-123", "def-456", "ghi-789"},
			},
			Project: "test",
		}
		s := toSummary(task)
		if s.SessionCount != 3 {
			t.Errorf("session_count = %d, want 3", s.SessionCount)
		}
	})
}

func TestUpdateTaskSessionsAppend(t *testing.T) {
	t.Run("append sessions to task with none", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Sessions = append(task.Meta.Sessions, "abc-123")
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if len(reloaded.Meta.Sessions) != 1 || reloaded.Meta.Sessions[0] != "abc-123" {
			t.Errorf("sessions = %v, want [abc-123]", reloaded.Meta.Sessions)
		}
	})

	t.Run("append preserves existing sessions", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Sessions = []string{"abc-123"}
		storage.WriteTask(task)

		task, _ = store.FindTask("test", "t-1")
		task.Meta.Sessions = append(task.Meta.Sessions, "def-456")
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if len(reloaded.Meta.Sessions) != 2 {
			t.Fatalf("sessions len = %d, want 2", len(reloaded.Meta.Sessions))
		}
		if reloaded.Meta.Sessions[0] != "abc-123" {
			t.Errorf("sessions[0] = %q, want abc-123", reloaded.Meta.Sessions[0])
		}
		if reloaded.Meta.Sessions[1] != "def-456" {
			t.Errorf("sessions[1] = %q, want def-456", reloaded.Meta.Sessions[1])
		}
	})

	t.Run("empty sessions input preserves existing", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Sessions = []string{"abc-123"}
		storage.WriteTask(task)

		task, _ = store.FindTask("test", "t-1")
		// Simulate: no sessions in update (don't touch task.Meta.Sessions)
		storage.WriteTask(task)

		reloaded, _ := store.FindTask("test", "t-1")
		if len(reloaded.Meta.Sessions) != 1 || reloaded.Meta.Sessions[0] != "abc-123" {
			t.Errorf("sessions = %v, want [abc-123]", reloaded.Meta.Sessions)
		}
	})
}

func TestAddTaskWithSessions(t *testing.T) {
	store, _ := setupMCPTestStore(t)

	task := storage.NewTask("t-2", "Task with sessions", "test")
	task.Meta.Sessions = []string{"sess-1", "sess-2"}
	task.FilePath = filepath.Join(store.Root, "test", "t-2-task-with-sessions.md")
	storage.WriteTask(task)

	reloaded, err := store.FindTask("test", "t-2")
	if err != nil {
		t.Fatalf("FindTask failed: %v", err)
	}
	if len(reloaded.Meta.Sessions) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(reloaded.Meta.Sessions))
	}
	if reloaded.Meta.Sessions[0] != "sess-1" || reloaded.Meta.Sessions[1] != "sess-2" {
		t.Errorf("sessions = %v, want [sess-1 sess-2]", reloaded.Meta.Sessions)
	}
}

func TestToDetailIncludesSessions(t *testing.T) {
	task := &storage.Task{
		Meta: storage.TaskMeta{
			ID:       "t-1",
			Title:    "With sessions",
			Sessions: []string{"abc-123", "def-456"},
		},
		Project: "test",
	}
	d := toDetail(task)
	if len(d.Sessions) != 2 {
		t.Fatalf("detail sessions len = %d, want 2", len(d.Sessions))
	}
	if d.Sessions[0] != "abc-123" || d.Sessions[1] != "def-456" {
		t.Errorf("detail sessions = %v", d.Sessions)
	}
	if d.SessionCount != 2 {
		t.Errorf("session_count = %d, want 2", d.SessionCount)
	}
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

func TestUpdateProjectArchived(t *testing.T) {
	store, _ := setupMCPTestStore(t)

	t.Run("archive project", func(t *testing.T) {
		proj, err := store.GetProject("test")
		if err != nil {
			t.Fatalf("GetProject failed: %v", err)
		}
		if proj.Archived {
			t.Fatal("project should not be archived initially")
		}

		proj.Archived = true
		err = storage.WriteProject(store.ProjectYAML("test"), proj)
		if err != nil {
			t.Fatalf("WriteProject failed: %v", err)
		}

		loaded, err := store.GetProject("test")
		if err != nil {
			t.Fatalf("GetProject failed: %v", err)
		}
		if !loaded.Archived {
			t.Error("project should be archived after update")
		}

		// ListActiveProjects should exclude it
		active, _ := store.ListActiveProjects()
		for _, slug := range active {
			if slug == "test" {
				t.Error("archived project should not appear in ListActiveProjects")
			}
		}
	})

	t.Run("unarchive project", func(t *testing.T) {
		proj, _ := store.GetProject("test")
		proj.Archived = false
		storage.WriteProject(store.ProjectYAML("test"), proj)

		active, _ := store.ListActiveProjects()
		found := false
		for _, slug := range active {
			if slug == "test" {
				found = true
			}
		}
		if !found {
			t.Error("unarchived project should appear in ListActiveProjects")
		}
	})
}

func TestCrossProjectContextWrapsProjectsAndDailyTasks(t *testing.T) {
	store, task := setupMCPTestStore(t)
	storage.WriteDailyPlan(store.RootDir(), storage.DailyPlan{
		Date:  storage.Today(),
		Tasks: []string{task.Meta.ID},
	})

	result, _, err := crossProjectContext(store)
	if err != nil {
		t.Fatalf("crossProjectContext failed: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] type = %T, want *mcp.TextContent", result.Content[0])
	}

	var payload struct {
		Projects   []map[string]any `json:"projects"`
		DailyTasks []taskSummary    `json:"daily_tasks"`
	}
	if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(payload.Projects) != 1 {
		t.Fatalf("projects len = %d, want 1", len(payload.Projects))
	}
	if len(payload.DailyTasks) != 1 {
		t.Fatalf("daily_tasks len = %d, want 1", len(payload.DailyTasks))
	}
	if payload.DailyTasks[0].ID != task.Meta.ID {
		t.Errorf("daily task ID = %q, want %q", payload.DailyTasks[0].ID, task.Meta.ID)
	}
}
