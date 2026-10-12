package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// setupMCPTestStore creates a temp store with a project and a task.
func setupMCPTestStore(t *testing.T) (*storage.Store, *storage.Task) {
	t.Helper()
	dir := t.TempDir()
	store := &storage.Store{Root: dir}

	projDir := filepath.Join(dir, "test")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteProject(filepath.Join(projDir, "project.yaml"), &storage.Project{
		Name:   "Test Project",
		Prefix: "t",
		Path:   "/home/user/test",
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
			AC:      "- Login works\n- Error shown on invalid creds",
		},
		Body:     "## Description\n\nOriginal body",
		FilePath: filepath.Join(projDir, "t-1-test-task.md"),
		Project:  "test",
	}
	if err := storage.WriteTask(task); err != nil {
		t.Fatal(err)
	}

	return store, task
}

// TestStorageCreateProjectAllowsFreshSlug pins a storage-layer invariant:
// Store.CreateProject itself has no duplicate-slug guard (that check lives
// only in the pm_create_project MCP handler - see
// TestE2ECreateProject/duplicate_slug_rejected for real handler coverage), so
// this just confirms a project with a brand-new slug is created and readable
// back, alongside a pre-existing one.
func TestStorageCreateProjectAllowsFreshSlug(t *testing.T) {
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

func TestModeInvariants(t *testing.T) {
	t.Run("mode round-trips through write and reload", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Mode = "manual"
		if err := storage.WriteTask(task); err != nil {
			t.Fatalf("write: %v", err)
		}
		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Mode != "manual" {
			t.Errorf("mode = %q, want manual", reloaded.Meta.Mode)
		}
	})

	t.Run("invalid mode rejected at write", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		task, _ := store.FindTask("test", "t-1")

		task.Meta.Mode = "sometimes"
		if err := storage.WriteTask(task); err == nil {
			t.Fatal("expected write to reject invalid mode")
		}
		// on-disk task must be untouched
		reloaded, _ := store.FindTask("test", "t-1")
		if reloaded.Meta.Mode != "" {
			t.Errorf("on-disk mode = %q, want empty (write must not go through)", reloaded.Meta.Mode)
		}
	})

	t.Run("mode preserved on status transitions", func(t *testing.T) {
		store, _ := setupMCPTestStore(t)
		sess := startMCP(t, store)
		mustCall(t, sess, "pm_update_task", map[string]any{"project": "test", "task_id": "t-1", "mode": "manual"})
		mustCall(t, sess, "pm_move_task", map[string]any{"project": "test", "task_id": "t-1", "new_status": "done"})
		var got struct {
			Mode string `json:"mode"`
		}
		mustUnmarshal(t, mustCall(t, sess, "pm_get_task", map[string]any{"project": "test", "task_id": "t-1"}), &got)
		if got.Mode != "manual" {
			t.Errorf("mode lost on status transition, got %q", got.Mode)
		}
	})

	t.Run("toDetail includes mode", func(t *testing.T) {
		task := &storage.Task{Meta: storage.TaskMeta{ID: "t-9", Mode: "manual"}, Project: "test"}
		if d := toDetail(task); d.Mode != "manual" {
			t.Errorf("detail mode = %q, want manual", d.Mode)
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

	t.Run("sibling dir with shared name prefix does not match", func(t *testing.T) {
		slug, err := resolveProjectFromCwd(store, "/home/user/code/myproject-old/src")
		if err == nil {
			t.Errorf("expected no match, got slug %q", slug)
		}
	})

	t.Run("longest matching project wins over a shallower one", func(t *testing.T) {
		dir2 := t.TempDir()
		store2 := &storage.Store{Root: dir2}

		shallowDir := filepath.Join(dir2, "code")
		os.MkdirAll(shallowDir, 0755)
		storage.WriteProject(filepath.Join(shallowDir, "project.yaml"), &storage.Project{
			Name: "Code",
			Path: "/home/user/code",
		})

		deepDir := filepath.Join(dir2, "pmcli")
		os.MkdirAll(deepDir, 0755)
		storage.WriteProject(filepath.Join(deepDir, "project.yaml"), &storage.Project{
			Name: "PM CLI",
			Path: "/home/user/code/pm-cli",
		})

		slug, err := resolveProjectFromCwd(store2, "/home/user/code/pm-cli/internal")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slug != "pmcli" {
			t.Errorf("got %q, want %q (deepest/longest match)", slug, "pmcli")
		}
	})

	t.Run("tilde-expanded project path matches", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory available")
		}

		dir3 := t.TempDir()
		store3 := &storage.Store{Root: dir3}

		projDir := filepath.Join(dir3, "tildeproj")
		os.MkdirAll(projDir, 0755)
		storage.WriteProject(filepath.Join(projDir, "project.yaml"), &storage.Project{
			Name: "Tilde Project",
			Path: "~/repos/tildeproj",
		})

		slug, err := resolveProjectFromCwd(store3, filepath.Join(home, "repos", "tildeproj", "src"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if slug != "tildeproj" {
			t.Errorf("got %q, want %q", slug, "tildeproj")
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

func TestCrossProjectContextWrapsProjectsAndFocusTasks(t *testing.T) {
	store, task := setupMCPTestStore(t)
	storage.WriteFocusPlan(store.RootDir(), storage.FocusPlan{
		Date:  storage.Today(),
		Tasks: []string{task.Meta.ID},
	})

	result, _, err := crossProjectContext(store, "")
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
		FocusTasks []taskSummary    `json:"focus_tasks"`
	}
	if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(payload.Projects) != 1 {
		t.Fatalf("projects len = %d, want 1", len(payload.Projects))
	}
	if len(payload.FocusTasks) != 1 {
		t.Fatalf("focus_tasks len = %d, want 1", len(payload.FocusTasks))
	}
	if payload.FocusTasks[0].ID != task.Meta.ID {
		t.Errorf("focus task ID = %q, want %q", payload.FocusTasks[0].ID, task.Meta.ID)
	}
}
