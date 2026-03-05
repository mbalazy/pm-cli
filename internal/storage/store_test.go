package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestStore creates a temp store with a project and optional tasks.
func setupTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store := &Store{Root: dir}

	// Create project "alpha" with prefix "a"
	alphaDir := filepath.Join(dir, "alpha")
	os.MkdirAll(alphaDir, 0755)
	WriteProject(filepath.Join(alphaDir, "project.yaml"), &Project{
		Name:   "Alpha Project",
		Prefix: "a",
		Path:   "/home/user/projects/alpha",
	})

	// Create some tasks
	for _, task := range []*Task{
		{
			Meta:     TaskMeta{ID: "a-1", Title: "First task", Status: StatusTodo, Created: "2025-01-01", Updated: "2025-01-01"},
			FilePath: filepath.Join(alphaDir, "a-1-first-task.md"),
		},
		{
			Meta:     TaskMeta{ID: "a-2", Title: "Second task", Status: StatusDoing, Created: "2025-01-02", Updated: "2025-01-02", Brief: "working on it"},
			FilePath: filepath.Join(alphaDir, "a-2-second-task.md"),
		},
		{
			Meta:     TaskMeta{ID: "a-3", Title: "Third task", Status: StatusDone, Created: "2025-01-03", Updated: "2025-01-03"},
			FilePath: filepath.Join(alphaDir, "a-3-third-task.md"),
		},
	} {
		WriteTask(task)
	}

	return store, dir
}

func TestNextTaskID(t *testing.T) {
	store, dir := setupTestStore(t)

	t.Run("sequential after existing", func(t *testing.T) {
		got := store.NextTaskID("alpha")
		if got != "a-4" {
			t.Errorf("NextTaskID = %q, want %q", got, "a-4")
		}
	})

	t.Run("empty project returns 1", func(t *testing.T) {
		emptyDir := filepath.Join(dir, "empty")
		os.MkdirAll(emptyDir, 0755)
		WriteProject(filepath.Join(emptyDir, "project.yaml"), &Project{Name: "Empty", Prefix: "e"})
		got := store.NextTaskID("empty")
		if got != "e-1" {
			t.Errorf("NextTaskID = %q, want %q", got, "e-1")
		}
	})

	t.Run("uses prefix from project.yaml", func(t *testing.T) {
		got := store.NextTaskID("alpha")
		if !strings.HasPrefix(got, "a-") {
			t.Errorf("NextTaskID should use prefix 'a-', got %q", got)
		}
	})

	t.Run("falls back to slug when no prefix", func(t *testing.T) {
		noPrefix := filepath.Join(dir, "beta")
		os.MkdirAll(noPrefix, 0755)
		WriteProject(filepath.Join(noPrefix, "project.yaml"), &Project{Name: "Beta"})
		got := store.NextTaskID("beta")
		if got != "beta-1" {
			t.Errorf("NextTaskID = %q, want %q", got, "beta-1")
		}
	})

	t.Run("skips non-numeric suffixes", func(t *testing.T) {
		// Add a task with a non-numeric ID suffix
		weirdTask := &Task{
			Meta:     TaskMeta{ID: "a-foo", Title: "Weird", Status: StatusTodo, Created: "2025-01-01", Updated: "2025-01-01"},
			FilePath: filepath.Join(dir, "alpha", "a-foo-weird.md"),
		}
		WriteTask(weirdTask)
		got := store.NextTaskID("alpha")
		// Should still be a-4, ignoring a-foo
		if got != "a-4" {
			t.Errorf("NextTaskID = %q, want %q", got, "a-4")
		}
	})
}

func TestAddTaskPreventsDuplicates(t *testing.T) {
	store, _ := setupTestStore(t)

	t.Run("duplicate filename rejected", func(t *testing.T) {
		dup := NewTask("a-1", "First task", "alpha")
		err := store.AddTask("alpha", dup)
		if err == nil {
			t.Error("expected error for duplicate filename")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("error should mention 'already exists', got: %v", err)
		}
	})

	t.Run("unique task succeeds", func(t *testing.T) {
		unique := NewTask("a-99", "Unique Task", "alpha")
		err := store.AddTask("alpha", unique)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestResolveProject(t *testing.T) {
	store, dir := setupTestStore(t)

	// Create a second project for ambiguity tests
	alphatwo := filepath.Join(dir, "alphatwo")
	os.MkdirAll(alphatwo, 0755)
	WriteProject(filepath.Join(alphatwo, "project.yaml"), &Project{Name: "Alpha Two"})

	t.Run("exact match", func(t *testing.T) {
		got, err := store.ResolveProject("alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "alpha" {
			t.Errorf("got %q, want %q", got, "alpha")
		}
	})

	t.Run("exact match wins over prefix", func(t *testing.T) {
		// "alpha" matches both "alpha" and "alphatwo", but exact match wins
		got, err := store.ResolveProject("alpha")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "alpha" {
			t.Errorf("got %q, want %q", got, "alpha")
		}
	})

	t.Run("unique prefix", func(t *testing.T) {
		got, err := store.ResolveProject("alphat")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "alphatwo" {
			t.Errorf("got %q, want %q", got, "alphatwo")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		got, err := store.ResolveProject("ALPHA")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// "alpha" lowercased matches prefix of both "alpha" and "alphatwo",
		// but exact match on "alpha" wins
		if got != "alpha" {
			t.Errorf("got %q, want %q", got, "alpha")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.ResolveProject("nonexistent")
		if err == nil {
			t.Error("expected error for nonexistent project")
		}
	})

	t.Run("ambiguous prefix", func(t *testing.T) {
		// Create another "al" project to make "al" ambiguous
		alDir := filepath.Join(dir, "albeta")
		os.MkdirAll(alDir, 0755)
		WriteProject(filepath.Join(alDir, "project.yaml"), &Project{Name: "Al Beta"})

		_, err := store.ResolveProject("al")
		if err == nil {
			t.Error("expected error for ambiguous prefix")
		}
		if !strings.Contains(err.Error(), "ambiguous") {
			t.Errorf("error should mention 'ambiguous', got: %v", err)
		}
	})
}

func TestFindTask(t *testing.T) {
	store, _ := setupTestStore(t)

	t.Run("exact ID match", func(t *testing.T) {
		task, err := store.FindTask("alpha", "a-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.ID != "a-1" {
			t.Errorf("got ID %q, want %q", task.Meta.ID, "a-1")
		}
	})

	t.Run("ID prefix match", func(t *testing.T) {
		task, err := store.FindTask("alpha", "a-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.ID != "a-1" {
			t.Errorf("got ID %q, want %q", task.Meta.ID, "a-1")
		}
	})

	t.Run("case insensitive ID", func(t *testing.T) {
		task, err := store.FindTask("alpha", "A-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.ID != "a-1" {
			t.Errorf("got ID %q, want %q", task.Meta.ID, "a-1")
		}
	})

	t.Run("title substring", func(t *testing.T) {
		task, err := store.FindTask("alpha", "first")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.ID != "a-1" {
			t.Errorf("got ID %q, want %q", task.Meta.ID, "a-1")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.FindTask("alpha", "nonexistent-xyz")
		if err == nil {
			t.Error("expected error for nonexistent task")
		}
	})

	t.Run("ambiguous title", func(t *testing.T) {
		// "task" matches all three titles
		_, err := store.FindTask("alpha", "task")
		if err == nil {
			t.Error("expected error for ambiguous match")
		}
		if !strings.Contains(err.Error(), "ambiguous") {
			t.Errorf("error should mention 'ambiguous', got: %v", err)
		}
	})

	t.Run("ambiguous prefix", func(t *testing.T) {
		// "a-" matches all three IDs
		_, err := store.FindTask("alpha", "a-")
		if err == nil {
			t.Error("expected error for ambiguous prefix match")
		}
	})
}

func TestGetProjectStatuses(t *testing.T) {
	store, dir := setupTestStore(t)

	t.Run("default statuses", func(t *testing.T) {
		statuses := store.GetProjectStatuses("alpha")
		if len(statuses) != len(DefaultStatuses) {
			t.Errorf("got %d statuses, want %d", len(statuses), len(DefaultStatuses))
		}
		for i, s := range DefaultStatuses {
			if statuses[i] != s {
				t.Errorf("status[%d] = %q, want %q", i, statuses[i], s)
			}
		}
	})

	t.Run("custom statuses from project.yaml", func(t *testing.T) {
		customDir := filepath.Join(dir, "custom")
		os.MkdirAll(customDir, 0755)
		WriteProject(filepath.Join(customDir, "project.yaml"), &Project{
			Name:     "Custom",
			Statuses: []string{"backlog", "in-progress", "review", "shipped"},
		})
		statuses := store.GetProjectStatuses("custom")
		if len(statuses) != 4 {
			t.Fatalf("got %d statuses, want 4", len(statuses))
		}
		if statuses[0] != "backlog" {
			t.Errorf("first status = %q, want %q", statuses[0], "backlog")
		}
	})

	t.Run("non-existent project returns defaults", func(t *testing.T) {
		statuses := store.GetProjectStatuses("noexist")
		if len(statuses) != len(DefaultStatuses) {
			t.Errorf("got %d statuses, want %d defaults", len(statuses), len(DefaultStatuses))
		}
	})

	t.Run("default statuses returned as copy not reference", func(t *testing.T) {
		statuses := store.GetProjectStatuses("alpha")
		original := make([]TaskStatus, len(DefaultStatuses))
		copy(original, DefaultStatuses)

		// Mutate the returned slice
		statuses[2] = "mutated"

		// DefaultStatuses must not be affected
		for i, s := range DefaultStatuses {
			if s != original[i] {
				t.Errorf("DefaultStatuses[%d] = %q, was mutated (want %q)", i, s, original[i])
			}
		}
	})
}

func TestGetAllStatuses(t *testing.T) {
	store, dir := setupTestStore(t)

	t.Run("union across projects", func(t *testing.T) {
		customDir := filepath.Join(dir, "custom")
		os.MkdirAll(customDir, 0755)
		WriteProject(filepath.Join(customDir, "project.yaml"), &Project{
			Name:     "Custom",
			Statuses: []string{"todo", "review", "done"},
		})

		statuses := store.GetAllStatuses()
		// Should have defaults + "review"
		found := false
		for _, s := range statuses {
			if s == "review" {
				found = true
			}
		}
		if !found {
			t.Error("expected 'review' in union of all statuses")
		}
	})

	t.Run("no projects returns defaults", func(t *testing.T) {
		emptyStore := &Store{Root: t.TempDir()}
		statuses := emptyStore.GetAllStatuses()
		if len(statuses) != len(DefaultStatuses) {
			t.Errorf("got %d statuses, want %d defaults", len(statuses), len(DefaultStatuses))
		}
	})
}

func TestMoveTask(t *testing.T) {
	t.Run("updates status and timestamp", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-1")

		err := store.MoveTask(task, StatusDoing)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.Status != StatusDoing {
			t.Errorf("status = %q, want %q", task.Meta.Status, StatusDoing)
		}
		if task.Meta.Updated != Today() {
			t.Errorf("updated = %q, want %q", task.Meta.Updated, Today())
		}
	})

	t.Run("preserves brief on done", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-2") // has brief "working on it"

		err := store.MoveTask(task, StatusDone)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.Brief != "working on it" {
			t.Errorf("brief = %q, want %q", task.Meta.Brief, "working on it")
		}
		reloaded, _ := store.FindTask("alpha", "a-2")
		if reloaded.Meta.Brief != "working on it" {
			t.Errorf("persisted brief = %q, want %q", reloaded.Meta.Brief, "working on it")
		}
	})

	t.Run("preserves brief on archived", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-2")

		err := store.MoveTask(task, StatusArchived)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.Brief != "working on it" {
			t.Errorf("brief = %q, want %q", task.Meta.Brief, "working on it")
		}
	})

	t.Run("preserves brief on other statuses", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-2")

		err := store.MoveTask(task, StatusWaiting)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.Brief != "working on it" {
			t.Errorf("brief should be preserved, got %q", task.Meta.Brief)
		}
	})

	t.Run("persists to disk", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-1")

		store.MoveTask(task, StatusDoing)

		reloaded, _ := store.FindTask("alpha", "a-1")
		if reloaded.Meta.Status != StatusDoing {
			t.Errorf("persisted status = %q, want %q", reloaded.Meta.Status, StatusDoing)
		}
	})

	t.Run("preserves brief on done", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-2") // brief: "working on it"

		err := store.MoveTask(task, StatusDone)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.Brief != "working on it" {
			t.Errorf("brief = %q, want %q", task.Meta.Brief, "working on it")
		}
		reloaded, _ := store.FindTask("alpha", "a-2")
		if reloaded.Meta.Brief != "working on it" {
			t.Errorf("persisted brief = %q, want %q", reloaded.Meta.Brief, "working on it")
		}
	})

	t.Run("preserves brief on archived", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-2")

		err := store.MoveTask(task, StatusArchived)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if task.Meta.Brief != "working on it" {
			t.Errorf("brief = %q, want %q", task.Meta.Brief, "working on it")
		}
	})
}

func TestStoreOperationsOnEmptyRoot(t *testing.T) {
	emptyStore := &Store{Root: t.TempDir()}

	t.Run("ListProjects returns empty", func(t *testing.T) {
		projects, err := emptyStore.ListProjects()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(projects) != 0 {
			t.Errorf("got %d projects, want 0", len(projects))
		}
	})

	t.Run("GetAllTasks returns empty", func(t *testing.T) {
		tasks, err := emptyStore.GetAllTasks()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(tasks) != 0 {
			t.Errorf("got %d tasks, want 0", len(tasks))
		}
	})

	t.Run("ResolveProject returns error", func(t *testing.T) {
		_, err := emptyStore.ResolveProject("anything")
		if err == nil {
			t.Error("expected error")
		}
	})
}

func TestListActiveProjects(t *testing.T) {
	store, dir := setupTestStore(t)

	// Create an archived project
	archivedDir := filepath.Join(dir, "old-project")
	os.MkdirAll(archivedDir, 0755)
	WriteProject(filepath.Join(archivedDir, "project.yaml"), &Project{
		Name:     "Old Project",
		Archived: true,
	})

	// Create another active project
	betaDir := filepath.Join(dir, "beta")
	os.MkdirAll(betaDir, 0755)
	WriteProject(filepath.Join(betaDir, "project.yaml"), &Project{
		Name: "Beta Project",
	})

	t.Run("excludes archived projects", func(t *testing.T) {
		active, err := store.ListActiveProjects()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, slug := range active {
			if slug == "old-project" {
				t.Error("archived project should not appear in active list")
			}
		}
		// alpha and beta should be present
		found := map[string]bool{}
		for _, slug := range active {
			found[slug] = true
		}
		if !found["alpha"] {
			t.Error("expected alpha in active projects")
		}
		if !found["beta"] {
			t.Error("expected beta in active projects")
		}
	})

	t.Run("ListProjects still returns all", func(t *testing.T) {
		all, err := store.ListProjects()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		found := map[string]bool{}
		for _, slug := range all {
			found[slug] = true
		}
		if !found["old-project"] {
			t.Error("expected old-project in all projects")
		}
		if !found["alpha"] {
			t.Error("expected alpha in all projects")
		}
	})

	t.Run("GetAllTasks excludes archived project tasks", func(t *testing.T) {
		// Add a task to the archived project
		WriteTask(&Task{
			Meta:     TaskMeta{ID: "old-1", Title: "Old task", Status: StatusTodo, Created: "2025-01-01", Updated: "2025-01-01"},
			FilePath: filepath.Join(archivedDir, "old-1-old-task.md"),
		})
		tasks, err := store.GetAllTasks()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, task := range tasks {
			if task.Project == "old-project" {
				t.Error("tasks from archived project should not appear in GetAllTasks")
			}
		}
	})
}
