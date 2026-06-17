package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFocusPlan_ReadMissing(t *testing.T) {
	fp := ReadFocusPlan(t.TempDir())
	if fp.Date != "" || len(fp.Tasks) != 0 {
		t.Fatalf("expected empty plan, got %+v", fp)
	}
}

func TestFocusPlan_ReadWriteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	plan := FocusPlan{
		Date:  "2026-04-07",
		Tasks: []string{"proj-1", "proj-2", "other-5"},
	}
	if err := WriteFocusPlan(dir, plan); err != nil {
		t.Fatal(err)
	}
	got := ReadFocusPlan(dir)
	if got.Date != plan.Date {
		t.Errorf("date: got %q, want %q", got.Date, plan.Date)
	}
	if len(got.Tasks) != 3 {
		t.Fatalf("tasks: got %d, want 3", len(got.Tasks))
	}
	for i, id := range plan.Tasks {
		if got.Tasks[i] != id {
			t.Errorf("tasks[%d]: got %q, want %q", i, got.Tasks[i], id)
		}
	}
}

func TestFocusPlan_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "focus.yaml"), []byte("not: [valid: yaml"), 0644)
	fp := ReadFocusPlan(dir)
	if fp.Date != "" || len(fp.Tasks) != 0 {
		t.Fatalf("expected empty plan on invalid yaml, got %+v", fp)
	}
}

func TestFocusPlan_Contains(t *testing.T) {
	fp := FocusPlan{Tasks: []string{"a-1", "b-2"}}
	if !fp.Contains("a-1") {
		t.Error("expected Contains(a-1) = true")
	}
	if fp.Contains("c-3") {
		t.Error("expected Contains(c-3) = false")
	}
}

func TestFocusPlan_Toggle(t *testing.T) {
	fp := FocusPlan{}

	fp.Toggle("a-1")
	if !fp.Contains("a-1") || len(fp.Tasks) != 1 {
		t.Fatalf("after add: %v", fp.Tasks)
	}

	fp.Toggle("a-1")
	if fp.Contains("a-1") || len(fp.Tasks) != 0 {
		t.Fatalf("after remove: %v", fp.Tasks)
	}

	fp.Toggle("a-1")
	if !fp.Contains("a-1") {
		t.Fatal("after re-add: missing")
	}
}

func TestFocusPlan_Remove(t *testing.T) {
	fp := FocusPlan{Tasks: []string{"a-1", "b-2", "c-3"}}
	fp.Remove("b-2")
	if len(fp.Tasks) != 2 || fp.Tasks[0] != "a-1" || fp.Tasks[1] != "c-3" {
		t.Fatalf("after remove: %v", fp.Tasks)
	}
	// removing non-existent is a no-op
	fp.Remove("z-99")
	if len(fp.Tasks) != 2 {
		t.Fatalf("remove non-existent changed tasks: %v", fp.Tasks)
	}
}

func TestFocusPlan_Swap(t *testing.T) {
	fp := FocusPlan{Tasks: []string{"a-1", "b-2", "c-3"}}
	fp.Swap(0, 1)
	if fp.Tasks[0] != "b-2" || fp.Tasks[1] != "a-1" {
		t.Fatalf("after swap: %v", fp.Tasks)
	}
	// out of bounds is a no-op
	fp.Swap(-1, 0)
	fp.Swap(0, 10)
	if fp.Tasks[0] != "b-2" {
		t.Fatal("out-of-bounds swap changed tasks")
	}
}

func TestFocusPlan_IsStale(t *testing.T) {
	tests := []struct {
		name  string
		date  string
		stale bool
	}{
		{"today", Today(), false},
		{"empty", "", false},
		{"yesterday", "2000-01-01", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := FocusPlan{Date: tt.date}
			if fp.IsStale() != tt.stale {
				t.Errorf("IsStale() = %v, want %v", fp.IsStale(), tt.stale)
			}
		})
	}
}

func TestFocusPlan_Cleanup(t *testing.T) {
	allTasks := []*Task{
		{Meta: TaskMeta{ID: "a-1", Status: StatusTodo}},
		{Meta: TaskMeta{ID: "b-2", Status: StatusDoing}},
		{Meta: TaskMeta{ID: "c-3", Status: StatusDone}},
		{Meta: TaskMeta{ID: "d-4", Status: StatusArchived}},
	}
	fp := FocusPlan{
		Date:  Today(),
		Tasks: []string{"a-1", "b-2", "c-3", "d-4", "missing-99"},
	}
	changed := fp.Cleanup(allTasks)
	if !changed {
		t.Error("expected changed=true")
	}
	if len(fp.Tasks) != 2 || fp.Tasks[0] != "a-1" || fp.Tasks[1] != "b-2" {
		t.Fatalf("after cleanup: %v (want [a-1 b-2])", fp.Tasks)
	}

	// no-op cleanup
	changed = fp.Cleanup(allTasks)
	if changed {
		t.Error("expected changed=false on second cleanup")
	}
}

func TestFocusPlan_MigrationFromDaily(t *testing.T) {
	dir := t.TempDir()
	// Write legacy daily.yaml
	legacy := filepath.Join(dir, "daily.yaml")
	os.WriteFile(legacy, []byte("date: \"2026-04-15\"\ntasks:\n    - proj-1\n"), 0644)

	// ReadFocusPlan should find it via fallback
	fp := ReadFocusPlan(dir)
	if fp.Date != "2026-04-15" || len(fp.Tasks) != 1 || fp.Tasks[0] != "proj-1" {
		t.Fatalf("migration read failed: %+v", fp)
	}

	// WriteFocusPlan should create focus.yaml and remove daily.yaml
	if err := WriteFocusPlan(dir, fp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("daily.yaml should be removed after migration write")
	}
	if _, err := os.Stat(filepath.Join(dir, "focus.yaml")); err != nil {
		t.Error("focus.yaml should exist after migration write")
	}
}
