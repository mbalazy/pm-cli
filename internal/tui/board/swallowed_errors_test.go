package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mbalazy/pm/internal/storage"
)

// TestLinksMenuOpenFailureToasts covers pm-cli-74-2: the links-menu "open"
// discarded exec.Command(...).Run()'s error with no channel to surface it.
// Switched to Start() (non-blocking, reaped via a background Wait()) plus a
// toast on failure.
func TestLinksMenuOpenFailureToasts(t *testing.T) {
	m := Model{}
	m.linkItems = []linkItem{{name: "docs", url: "https://example.com"}}
	m.linksCursor = 0
	m.linksMenu = true
	t.Setenv("PATH", "") // exec.LookPath("open") fails -> Start() returns an error

	result, _ := m.updateLinksMenu(tea.KeyMsg{Type: tea.KeyEnter})
	m2, ok := result.(Model)
	if !ok {
		t.Fatalf("updateLinksMenu returned %T, want Model", result)
	}
	if !strings.Contains(m2.toastMsg, "open link failed") {
		t.Errorf("toastMsg = %q, want it to mention the open failure", m2.toastMsg)
	}
}

// TestSaveFocusPlanToastsOnWriteFailure covers pm-cli-74-2: saveFocusPlan
// discarded WriteFocusPlan's error, so a failed write silently reverted the
// user's reorder/hide on the next reload() with no explanation.
func TestSaveFocusPlanToastsOnWriteFailure(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	// Put a directory where focus.yaml would be written, so the write fails.
	if err := os.MkdirAll(filepath.Join(store.Root, "focus.yaml"), 0755); err != nil {
		t.Fatal(err)
	}
	m := &Model{store: store}
	m.saveFocusPlan()
	if !strings.Contains(m.toastMsg, "focus plan") {
		t.Errorf("toastMsg = %q, want it to mention the focus plan write failure", m.toastMsg)
	}
}

// TestSaveTUIStateToastsOnWriteFailure is saveFocusPlan's sibling for the
// project-picker's hidden/order state (update_picker.go).
func TestSaveTUIStateToastsOnWriteFailure(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(store.Root, "tui-config.yaml"), 0755); err != nil {
		t.Fatal(err)
	}
	m := &Model{store: store, projects: []string{"all"}, hiddenProjects: map[string]bool{}}
	m.saveTUIState()
	if !strings.Contains(m.toastMsg, "failed") {
		t.Errorf("toastMsg = %q, want it to mention the save failure", m.toastMsg)
	}
}

// TestLaunchProjectClaudeMissingProjectToasts covers pm-cli-74-2: a
// project-scope "Here" launch whose project no longer resolves used to
// `return m, nil` with zero feedback.
func TestLaunchProjectClaudeMissingProjectToasts(t *testing.T) {
	store := &storage.Store{Root: t.TempDir()}
	m := Model{store: store, launchMenu: launchMenu{projectScopeLaunch: true, projectScopeSlug: "does-not-exist"}}
	result, _ := m.launchProjectClaude("here")
	m2, ok := result.(Model)
	if !ok {
		t.Fatalf("launchProjectClaude returned %T, want Model", result)
	}
	if !strings.Contains(m2.toastMsg, "launch failed") {
		t.Errorf("toastMsg = %q, want it to mention the launch failure", m2.toastMsg)
	}
}

// TestCopyWorktreeFilesReturnsErrorOnFailedGitAdd covers pm-cli-74-2:
// copyWorktreeFiles used to swallow a failed `git worktree add` and return
// early, and its caller (launchClaude's worktree/worktree-tmux kinds) then
// launched anyway into a worktree whose gitignored files were never seeded.
// The function must now report the failure so the caller can abort.
func TestCopyWorktreeFilesReturnsErrorOnFailedGitAdd(t *testing.T) {
	dir := t.TempDir() // not a git repo -> `git worktree add` fails
	if err := copyWorktreeFiles(dir, "some-branch"); err == nil {
		t.Fatal("expected an error when the source dir is not a git repo")
	}
}
