package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDailyPlan_ReadMissing(t *testing.T) {
	dp := ReadDailyPlan(t.TempDir())
	if dp.Date != "" || len(dp.Tasks) != 0 {
		t.Fatalf("expected empty plan, got %+v", dp)
	}
}

func TestDailyPlan_ReadWriteRoundtrip(t *testing.T) {
	dir := t.TempDir()
	plan := DailyPlan{
		Date:  "2026-04-07",
		Tasks: []string{"proj-1", "proj-2", "other-5"},
	}
	if err := WriteDailyPlan(dir, plan); err != nil {
		t.Fatal(err)
	}
	got := ReadDailyPlan(dir)
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

func TestDailyPlan_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "daily.yaml"), []byte("not: [valid: yaml"), 0644)
	dp := ReadDailyPlan(dir)
	if dp.Date != "" || len(dp.Tasks) != 0 {
		t.Fatalf("expected empty plan on invalid yaml, got %+v", dp)
	}
}

func TestDailyPlan_Contains(t *testing.T) {
	dp := DailyPlan{Tasks: []string{"a-1", "b-2"}}
	if !dp.Contains("a-1") {
		t.Error("expected Contains(a-1) = true")
	}
	if dp.Contains("c-3") {
		t.Error("expected Contains(c-3) = false")
	}
}

func TestDailyPlan_Toggle(t *testing.T) {
	dp := DailyPlan{}

	dp.Toggle("a-1")
	if !dp.Contains("a-1") || len(dp.Tasks) != 1 {
		t.Fatalf("after add: %v", dp.Tasks)
	}

	dp.Toggle("a-1")
	if dp.Contains("a-1") || len(dp.Tasks) != 0 {
		t.Fatalf("after remove: %v", dp.Tasks)
	}

	dp.Toggle("a-1")
	if !dp.Contains("a-1") {
		t.Fatal("after re-add: missing")
	}
}

func TestDailyPlan_Remove(t *testing.T) {
	dp := DailyPlan{Tasks: []string{"a-1", "b-2", "c-3"}}
	dp.Remove("b-2")
	if len(dp.Tasks) != 2 || dp.Tasks[0] != "a-1" || dp.Tasks[1] != "c-3" {
		t.Fatalf("after remove: %v", dp.Tasks)
	}
	// removing non-existent is a no-op
	dp.Remove("z-99")
	if len(dp.Tasks) != 2 {
		t.Fatalf("remove non-existent changed tasks: %v", dp.Tasks)
	}
}

func TestDailyPlan_Swap(t *testing.T) {
	dp := DailyPlan{Tasks: []string{"a-1", "b-2", "c-3"}}
	dp.Swap(0, 1)
	if dp.Tasks[0] != "b-2" || dp.Tasks[1] != "a-1" {
		t.Fatalf("after swap: %v", dp.Tasks)
	}
	// out of bounds is a no-op
	dp.Swap(-1, 0)
	dp.Swap(0, 10)
	if dp.Tasks[0] != "b-2" {
		t.Fatal("out-of-bounds swap changed tasks")
	}
}

func TestDailyPlan_IsStale(t *testing.T) {
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
			dp := DailyPlan{Date: tt.date}
			if dp.IsStale() != tt.stale {
				t.Errorf("IsStale() = %v, want %v", dp.IsStale(), tt.stale)
			}
		})
	}
}

func TestDailyPlan_Cleanup(t *testing.T) {
	allTasks := []*Task{
		{Meta: TaskMeta{ID: "a-1", Status: StatusTodo}},
		{Meta: TaskMeta{ID: "b-2", Status: StatusDoing}},
		{Meta: TaskMeta{ID: "c-3", Status: StatusDone}},
		{Meta: TaskMeta{ID: "d-4", Status: StatusArchived}},
	}
	dp := DailyPlan{
		Date:  Today(),
		Tasks: []string{"a-1", "b-2", "c-3", "d-4", "missing-99"},
	}
	changed := dp.Cleanup(allTasks)
	if !changed {
		t.Error("expected changed=true")
	}
	if len(dp.Tasks) != 2 || dp.Tasks[0] != "a-1" || dp.Tasks[1] != "b-2" {
		t.Fatalf("after cleanup: %v (want [a-1 b-2])", dp.Tasks)
	}

	// no-op cleanup
	changed = dp.Cleanup(allTasks)
	if changed {
		t.Error("expected changed=false on second cleanup")
	}
}
