package board

import (
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// TestOpenClaudeMenuResetsAdditionalToggle covers pm-cli-74-2's stale-toggle
// leak: openExecutorMenu already reset claudeMenuAdditional and
// executorAdditionalAvail, but openClaudeMenu only reset claudeMenuSkipPerms.
// Sequence: task X -> executor menu -> toggle "#" on -> Esc -> open the plain
// Claude menu (c) on a DIFFERENT task -> @-cycle to executor. Before the fix,
// that launch used the isolated worktree without the user ever opting in
// from THIS menu.
func TestOpenClaudeMenuResetsAdditionalToggle(t *testing.T) {
	task := &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Test task"}}
	m := &Model{}
	// Simulate the stale state a prior executor-mode launch (on another task)
	// left behind.
	m.claudeMenuAdditional = true
	m.executorAdditionalAvail = true

	m.openClaudeMenu(task)

	if m.claudeMenuAdditional {
		t.Error("claudeMenuAdditional should reset to false when opening the plain Claude menu")
	}
	if m.executorAdditionalAvail {
		t.Error("executorAdditionalAvail should reset to false when opening the plain Claude menu")
	}
}
