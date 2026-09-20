package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm-cli/internal/storage"
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

// menuKinds flattens the launch-menu items to their kind strings.
func menuKinds(items []claudeMenuItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.kind)
	}
	return out
}

// TestExecutorMenuFinishItem verifies the acceptance launch entry: a tracker's
// executor menu offers "finish", a leaf task's does not.
func TestExecutorMenuFinishItem(t *testing.T) {
	t.Setenv("TMUX", "") // deterministic menu shape (no tmux items)

	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "app-1"}, Project: "app"}
	child := &storage.Task{Meta: storage.TaskMeta{ID: "app-1-1", Parent: "app-1"}, Project: "app"}
	m := &Model{tasks: []*storage.Task{tracker, child}}

	m.launchAgent = launchAgentExecutor
	m.rebuildClaudeMenuItems(tracker)
	kinds := menuKinds(m.claudeMenuItems)
	if strings.Join(kinds, " ") != "bg here finish dry-run" {
		t.Fatalf("tracker executor menu kinds = %v, want [bg here finish dry-run]", kinds)
	}

	m.rebuildClaudeMenuItems(child)
	kinds = menuKinds(m.claudeMenuItems)
	if strings.Join(kinds, " ") != "bg here dry-run" {
		t.Fatalf("leaf executor menu kinds = %v, want [bg here dry-run]", kinds)
	}
}

// TestThenFinishToggle verifies the & toggle: flips only in executor mode on a
// tracker, stays off everywhere else, and resets when the menu reopens.
func TestThenFinishToggle(t *testing.T) {
	amp := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'&'}}

	m := Model{}
	m.launchAgent = launchAgentExecutor
	m.executorIsTracker = true
	next, _ := m.updateClaudeMenu(amp)
	m = next.(Model)
	if !m.claudeMenuThenFinish {
		t.Fatal("& on an executor tracker must enable then-finish")
	}
	next, _ = m.updateClaudeMenu(amp)
	m = next.(Model)
	if m.claudeMenuThenFinish {
		t.Fatal("& again must disable then-finish")
	}

	// Leaf task: `pm work` has no --then-finish, the toggle must be inert.
	m.executorIsTracker = false
	next, _ = m.updateClaudeMenu(amp)
	m = next.(Model)
	if m.claudeMenuThenFinish {
		t.Fatal("& on a leaf task must stay off")
	}

	// Non-executor agent: inert too.
	m.executorIsTracker = true
	m.launchAgent = launchAgentClaude
	next, _ = m.updateClaudeMenu(amp)
	m = next.(Model)
	if m.claudeMenuThenFinish {
		t.Fatal("& outside executor mode must stay off")
	}

	// Reopening the menu resets the choice - a stale toggle must not leak into
	// a later launch the user never opted into.
	m.claudeMenuThenFinish = true
	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "app-1"}, Project: "app"}
	(&m).openExecutorMenu(tracker)
	if m.claudeMenuThenFinish {
		t.Fatal("openExecutorMenu must reset then-finish")
	}
	m.claudeMenuThenFinish = true
	(&m).openClaudeMenu(tracker)
	if m.claudeMenuThenFinish {
		t.Fatal("openClaudeMenu must reset then-finish")
	}
}
