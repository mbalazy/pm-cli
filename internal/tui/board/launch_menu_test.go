package board

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// TestExecutorSlotStatuses verifies the launch-menu slot indicator's data:
// per-slot live holder (busy), with stale (dead-pid) locks reading as free.
func TestExecutorSlotStatuses(t *testing.T) {
	base := t.TempDir()
	slot1 := filepath.Join(base, "wt-1")
	slot2 := filepath.Join(base, "wt-2")
	proj := &storage.Project{Path: filepath.Join(base, "repo"), Executor: &storage.Executor{
		Enabled: true,
		Worktrees: []storage.WorktreeSlot{
			{Path: slot1},
			{Path: slot2},
		},
	}}

	// Slot 1 busy (live foreign pid = the test runner's parent), slot 2 has a
	// STALE lock (dead pid) and must read as free.
	os.MkdirAll(slot1, 0755)
	os.MkdirAll(slot2, 0755)
	if err := storage.AcquireWorktreeLock(slot1, "app-9", "run-epic", os.Getppid()); err != nil {
		t.Fatalf("seed live lock: %v", err)
	}
	if err := storage.AcquireWorktreeLock(slot2, "app-old", "work", 2147483646); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}

	got := executorSlotStatuses(proj)
	if len(got) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(got))
	}
	if got[0].holder == nil || got[0].holder.TaskID != "app-9" {
		t.Fatalf("slot 1 should be busy by app-9, got %+v", got[0].holder)
	}
	if got[1].holder != nil {
		t.Fatalf("slot 2 (stale lock) should read as free, got %+v", got[1].holder)
	}

	if executorSlotStatuses(nil) != nil {
		t.Fatal("nil project must yield nil")
	}
}
