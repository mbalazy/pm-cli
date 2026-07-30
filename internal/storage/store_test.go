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

func TestNextChildID(t *testing.T) {
	store, dir := setupTestStore(t)
	alphaDir := filepath.Join(dir, "alpha")

	t.Run("first child when none exist", func(t *testing.T) {
		if got := store.NextChildID("alpha", "a-2"); got != "a-2-1" {
			t.Errorf("NextChildID = %q, want %q", got, "a-2-1")
		}
	})

	t.Run("sequential after existing children", func(t *testing.T) {
		for _, id := range []string{"a-2-1", "a-2-2"} {
			WriteTask(&Task{
				Meta:     TaskMeta{ID: id, Title: id, Status: StatusTodo, Parent: "a-2", Created: "2025-01-04", Updated: "2025-01-04"},
				FilePath: filepath.Join(alphaDir, id+"-child.md"),
			})
		}
		if got := store.NextChildID("alpha", "a-2"); got != "a-2-3" {
			t.Errorf("NextChildID = %q, want %q", got, "a-2-3")
		}
	})

	t.Run("ignores grandchildren and other parents", func(t *testing.T) {
		// a-2-1-1 (grandchild) and a-3-1 (other parent) must not affect a-2's count
		WriteTask(&Task{
			Meta:     TaskMeta{ID: "a-2-1-1", Title: "grandchild", Status: StatusTodo, Parent: "a-2-1", Created: "2025-01-05", Updated: "2025-01-05"},
			FilePath: filepath.Join(alphaDir, "a-2-1-1-gc.md"),
		})
		WriteTask(&Task{
			Meta:     TaskMeta{ID: "a-3-1", Title: "other", Status: StatusTodo, Parent: "a-3", Created: "2025-01-05", Updated: "2025-01-05"},
			FilePath: filepath.Join(alphaDir, "a-3-1-other.md"),
		})
		if got := store.NextChildID("alpha", "a-2"); got != "a-2-3" {
			t.Errorf("NextChildID = %q, want %q (grandchild/other-parent leaked)", got, "a-2-3")
		}
	})
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

	t.Run("rejects a status outside the project's set", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-1")
		before := task.Meta.Status

		err := store.MoveTask(task, TaskStatus("dnoe")) // the classic typo
		if err == nil {
			t.Fatal("expected an invalid-status error")
		}
		reloaded, _ := store.FindTask("alpha", "a-1")
		if reloaded.Meta.Status != before {
			t.Errorf("rejected move must not touch the file: status = %q", reloaded.Meta.Status)
		}
	})

	t.Run("archived is always allowed (system-level)", func(t *testing.T) {
		store, _ := setupTestStore(t)
		task, _ := store.FindTask("alpha", "a-1")
		if err := store.MoveTask(task, StatusArchived); err != nil {
			t.Fatalf("archived must bypass project-status validation: %v", err)
		}
	})

	t.Run("custom project statuses are honored", func(t *testing.T) {
		store, dir := setupTestStore(t)
		projDir := filepath.Join(dir, "custom")
		os.MkdirAll(projDir, 0755)
		WriteProject(filepath.Join(projDir, "project.yaml"), &Project{
			Name: "Custom", Statuses: []string{"backlog", "shipped"},
		})
		task := &Task{Meta: TaskMeta{ID: "c-1", Title: "T", Status: "backlog"}}
		if err := store.AddTask("custom", task); err != nil {
			t.Fatal(err)
		}
		if err := store.MoveTask(task, TaskStatus("shipped")); err != nil {
			t.Fatalf("listed custom status must pass: %v", err)
		}
		if err := store.MoveTask(task, StatusDoing); err == nil {
			t.Fatal("default status not in the custom set must be rejected")
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

func TestAddTaskValidatesStatus(t *testing.T) {
	store, _ := setupTestStore(t)

	t.Run("valid status accepted", func(t *testing.T) {
		task := NewTask("a-10", "Valid status", "alpha")
		task.Meta.Status = StatusDoing
		err := store.AddTask("alpha", task)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid status rejected", func(t *testing.T) {
		task := NewTask("a-11", "Bad status", "alpha")
		task.Meta.Status = ParseStatus("banana")
		err := store.AddTask("alpha", task)
		if err == nil {
			t.Error("expected error for invalid status")
		}
		if !strings.Contains(err.Error(), "invalid status") {
			t.Errorf("error should mention 'invalid status', got: %v", err)
		}
	})

	t.Run("custom project status accepted", func(t *testing.T) {
		dir := store.ProjectDir("custom-st")
		os.MkdirAll(dir, 0755)
		WriteProject(filepath.Join(dir, "project.yaml"), &Project{
			Name:     "Custom Statuses",
			Prefix:   "cs",
			Statuses: []string{"backlog", "review", "shipped"},
		})
		task := NewTask("cs-1", "Custom status task", "custom-st")
		task.Meta.Status = ParseStatus("review")
		err := store.AddTask("custom-st", task)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestAddTaskAtomicDuplicate(t *testing.T) {
	store, _ := setupTestStore(t)

	t.Run("duplicate rejected via O_EXCL", func(t *testing.T) {
		// a-1 already exists from setupTestStore
		dup := NewTask("a-1", "First task", "alpha")
		err := store.AddTask("alpha", dup)
		if err == nil {
			t.Error("expected error for duplicate filename")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("error should mention 'already exists', got: %v", err)
		}
	})
}

func TestAddTaskCleansUpPhantomFileOnValidationFailure(t *testing.T) {
	store, _ := setupTestStore(t)

	t.Run("invalid epic_mode leaves no file and retry succeeds", func(t *testing.T) {
		task := NewTask("a-20", "Bad epic mode", "alpha")
		task.Meta.EpicMode = "Independent" // wrong case - only "independent" (or empty) is valid
		err := store.AddTask("alpha", task)
		if err == nil {
			t.Fatal("expected error for invalid epic_mode")
		}
		if !strings.Contains(err.Error(), "invalid epic_mode") {
			t.Errorf("error should mention 'invalid epic_mode', got: %v", err)
		}

		if _, statErr := os.Stat(task.FilePath); !os.IsNotExist(statErr) {
			t.Fatalf("expected no phantom file at %s, stat err: %v", task.FilePath, statErr)
		}

		tasks, err := store.GetTasks("alpha")
		if err != nil {
			t.Fatalf("GetTasks failed: %v", err)
		}
		for _, tk := range tasks {
			if tk.Meta.ID == "a-20" {
				t.Fatalf("phantom task a-20 should not be visible via GetTasks")
			}
		}

		retry := NewTask("a-20", "Bad epic mode", "alpha")
		retry.Meta.EpicMode = "independent"
		if err := store.AddTask("alpha", retry); err != nil {
			t.Fatalf("retry with valid epic_mode should succeed, got: %v", err)
		}
	})

	t.Run("invalid mode leaves no file and retry succeeds", func(t *testing.T) {
		task := NewTask("a-21", "Bad mode", "alpha")
		task.Meta.Mode = "sometimes"
		err := store.AddTask("alpha", task)
		if err == nil {
			t.Fatal("expected error for invalid mode")
		}
		if !strings.Contains(err.Error(), "invalid mode") {
			t.Errorf("error should mention 'invalid mode', got: %v", err)
		}

		if _, statErr := os.Stat(task.FilePath); !os.IsNotExist(statErr) {
			t.Fatalf("expected no phantom file at %s, stat err: %v", task.FilePath, statErr)
		}

		retry := NewTask("a-21", "Bad mode", "alpha")
		retry.Meta.Mode = "manual"
		if err := store.AddTask("alpha", retry); err != nil {
			t.Fatalf("retry with valid mode should succeed, got: %v", err)
		}
	})
}

func TestWriteTaskAtomicRoundtrip(t *testing.T) {
	dir := t.TempDir()
	task := &Task{
		Meta: TaskMeta{
			ID:      "at-1",
			Title:   "Atomic write",
			Status:  StatusTodo,
			Created: "2025-01-01",
			Updated: "2025-01-01",
		},
		FilePath: filepath.Join(dir, "at-1-atomic-write.md"),
		Body:     "test body",
	}

	if err := writeTask(task); err != nil {
		t.Fatalf("writeTask failed: %v", err)
	}

	// Verify file exists and content is correct
	got, err := ReadTask(task.FilePath)
	if err != nil {
		t.Fatalf("ReadTask failed: %v", err)
	}
	if got.Meta.ID != "at-1" {
		t.Errorf("ID = %q, want %q", got.Meta.ID, "at-1")
	}
	if got.Body != "test body" {
		t.Errorf("Body = %q, want %q", got.Body, "test body")
	}

	// No temp files left behind
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pm-tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
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
