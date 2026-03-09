package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupSyncTestStore(t *testing.T) (*SyncStore, *Store) {
	t.Helper()
	dir := t.TempDir()
	inner := &Store{Root: dir}

	// Create project
	alphaDir := filepath.Join(dir, "alpha")
	os.MkdirAll(alphaDir, 0755)
	WriteProject(filepath.Join(alphaDir, "project.yaml"), &Project{
		Name:   "Alpha",
		Prefix: "a",
	})

	// Create a task
	WriteTask(&Task{
		Meta:     TaskMeta{ID: "a-1", Title: "Test task", Status: StatusTodo, Created: "2025-01-01", Updated: "2025-01-01"},
		FilePath: filepath.Join(alphaDir, "a-1-test-task.md"),
	})

	ss := &SyncStore{
		TaskStore: inner,
		queuePath: filepath.Join(dir, ".sync-queue.jsonl"),
		client:    newSyncClient("http://localhost:9999", "test-token"),
		done:      make(chan struct{}),
	}
	// Don't start flushLoop in tests - we test queue directly

	return ss, inner
}

func readQueueOps(t *testing.T, path string) []SyncOp {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read queue: %v", err)
	}
	var ops []SyncOp
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var op SyncOp
		if err := json.Unmarshal([]byte(line), &op); err != nil {
			t.Fatalf("parse queue line: %v", err)
		}
		ops = append(ops, op)
	}
	return ops
}

func TestSyncStore_AddTask(t *testing.T) {
	ss, _ := setupSyncTestStore(t)

	task := NewTask("a-2", "New task", "alpha")
	err := ss.AddTask("alpha", task)
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	ops := readQueueOps(t, ss.queuePath)
	if len(ops) != 1 {
		t.Fatalf("expected 1 queue op, got %d", len(ops))
	}
	if ops[0].Op != "upsert" {
		t.Errorf("op = %q, want upsert", ops[0].Op)
	}
	if !strings.Contains(ops[0].File, "alpha/") {
		t.Errorf("file = %q, should contain alpha/", ops[0].File)
	}
}

func TestSyncStore_WriteTask(t *testing.T) {
	ss, inner := setupSyncTestStore(t)

	task, err := inner.FindTask("alpha", "a-1")
	if err != nil {
		t.Fatalf("FindTask: %v", err)
	}

	task.Meta.Brief = "updated brief"
	err = ss.WriteTask(task)
	if err != nil {
		t.Fatalf("WriteTask: %v", err)
	}

	ops := readQueueOps(t, ss.queuePath)
	if len(ops) != 1 {
		t.Fatalf("expected 1 queue op, got %d", len(ops))
	}
	if ops[0].Op != "upsert" {
		t.Errorf("op = %q, want upsert", ops[0].Op)
	}

	// Verify task was actually written to disk
	reloaded, _ := inner.FindTask("alpha", "a-1")
	if reloaded.Meta.Brief != "updated brief" {
		t.Errorf("brief = %q, want %q", reloaded.Meta.Brief, "updated brief")
	}
}

func TestSyncStore_MoveTask(t *testing.T) {
	ss, inner := setupSyncTestStore(t)

	task, _ := inner.FindTask("alpha", "a-1")
	err := ss.MoveTask(task, StatusDoing)
	if err != nil {
		t.Fatalf("MoveTask: %v", err)
	}

	ops := readQueueOps(t, ss.queuePath)
	if len(ops) != 1 {
		t.Fatalf("expected 1 queue op, got %d", len(ops))
	}

	reloaded, _ := inner.FindTask("alpha", "a-1")
	if reloaded.Meta.Status != StatusDoing {
		t.Errorf("status = %q, want %q", reloaded.Meta.Status, StatusDoing)
	}
}

func TestSyncStore_DeleteTask(t *testing.T) {
	ss, inner := setupSyncTestStore(t)

	task, _ := inner.FindTask("alpha", "a-1")
	err := ss.DeleteTask(task)
	if err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	ops := readQueueOps(t, ss.queuePath)
	if len(ops) != 1 {
		t.Fatalf("expected 1 queue op, got %d", len(ops))
	}
	if ops[0].Op != "delete" {
		t.Errorf("op = %q, want delete", ops[0].Op)
	}
}

func TestSyncStore_CreateProject(t *testing.T) {
	ss, _ := setupSyncTestStore(t)

	err := ss.CreateProject("beta", &Project{Name: "Beta"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	ops := readQueueOps(t, ss.queuePath)
	if len(ops) != 1 {
		t.Fatalf("expected 1 queue op, got %d", len(ops))
	}
	if ops[0].File != "beta/project.yaml" {
		t.Errorf("file = %q, want beta/project.yaml", ops[0].File)
	}
}

func TestSyncStore_UpdateProject(t *testing.T) {
	ss, inner := setupSyncTestStore(t)

	proj, _ := inner.GetProject("alpha")
	proj.Notes = "updated"
	err := ss.UpdateProject("alpha", proj)
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}

	ops := readQueueOps(t, ss.queuePath)
	if len(ops) != 1 {
		t.Fatalf("expected 1 queue op, got %d", len(ops))
	}
	if ops[0].File != "alpha/project.yaml" {
		t.Errorf("file = %q, want alpha/project.yaml", ops[0].File)
	}
}

func TestSyncStore_MultipleOps(t *testing.T) {
	ss, inner := setupSyncTestStore(t)

	// Multiple writes to same task
	task, _ := inner.FindTask("alpha", "a-1")
	task.Meta.Brief = "first"
	ss.WriteTask(task)
	task.Meta.Brief = "second"
	ss.WriteTask(task)
	task.Meta.Brief = "third"
	ss.WriteTask(task)

	ops := readQueueOps(t, ss.queuePath)
	if len(ops) != 3 {
		t.Fatalf("expected 3 queue ops, got %d", len(ops))
	}

	// Deduplicate should keep only last
	deduped := deduplicateOps(ops)
	if len(deduped) != 1 {
		t.Fatalf("expected 1 deduped op, got %d", len(deduped))
	}
	if deduped[0].TS != ops[2].TS {
		t.Error("deduplication should keep last op")
	}
}

func TestSyncStore_QueueLen(t *testing.T) {
	ss, inner := setupSyncTestStore(t)

	if ss.QueueLen() != 0 {
		t.Errorf("initial queue len = %d, want 0", ss.QueueLen())
	}

	task, _ := inner.FindTask("alpha", "a-1")
	ss.WriteTask(task)

	if ss.QueueLen() != 1 {
		t.Errorf("after write queue len = %d, want 1", ss.QueueLen())
	}
}

func TestSyncStore_ReadPassthrough(t *testing.T) {
	ss, inner := setupSyncTestStore(t)

	// Read operations should work through SyncStore
	tasks, err := ss.GetTasks("alpha")
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	innerTasks, _ := inner.GetTasks("alpha")
	if len(tasks) != len(innerTasks) {
		t.Errorf("GetTasks returned %d tasks via sync, %d via inner", len(tasks), len(innerTasks))
	}

	projects, err := ss.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0] != "alpha" {
		t.Errorf("ListProjects = %v, want [alpha]", projects)
	}
}

func TestDeduplicateOps(t *testing.T) {
	t.Run("keeps last per file", func(t *testing.T) {
		ops := []SyncOp{
			{Op: "upsert", File: "a/1.md", TS: "t1"},
			{Op: "upsert", File: "a/2.md", TS: "t2"},
			{Op: "upsert", File: "a/1.md", TS: "t3"},
		}
		got := deduplicateOps(ops)
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].TS != "t3" {
			t.Errorf("first op ts = %q, want t3 (latest for a/1.md)", got[0].TS)
		}
		if got[1].TS != "t2" {
			t.Errorf("second op ts = %q, want t2", got[1].TS)
		}
	})

	t.Run("delete overrides upsert", func(t *testing.T) {
		ops := []SyncOp{
			{Op: "upsert", File: "a/1.md", TS: "t1"},
			{Op: "delete", File: "a/1.md", TS: "t2"},
		}
		got := deduplicateOps(ops)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		if got[0].Op != "delete" {
			t.Errorf("op = %q, want delete", got[0].Op)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got := deduplicateOps(nil)
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})
}
