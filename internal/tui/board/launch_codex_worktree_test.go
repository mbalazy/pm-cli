package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// TestLaunchCodexWorktreeSetupFailureAborts covers pm-cli-76 (batched as
// pm-cli-132-5): launchCodex's "worktree" kind used to swallow
// copyWorktreeFiles' error and launch Codex anyway (falling back to the plain
// project dir when git worktree add failed, or into an unseeded worktree when
// only the untracked-file copy failed). It must now mirror launchClaude: on a
// worktree setup error, show a "worktree setup failed" toast and abort
// without launching anything.
func TestLaunchCodexWorktreeSetupFailureAborts(t *testing.T) {
	for _, kind := range []string{"worktree", "worktree-tmux"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir() // not a git repo -> `git worktree add` fails
			store := &storage.Store{Root: t.TempDir()}
			projDir := filepath.Join(store.Root, "proj")
			os.MkdirAll(projDir, 0755)
			storage.WriteProject(filepath.Join(projDir, "project.yaml"), &storage.Project{
				Name:   "Proj",
				Prefix: "proj",
				Path:   dir,
			})

			task := &storage.Task{Meta: storage.TaskMeta{ID: "proj-1", Title: "Some task"}, Project: "proj"}
			m := Model{
				store:       store,
				tasks:       []*storage.Task{task},
				currentView: viewDetail,
				detailState: detailState{detailTask: task},
			}

			result, cmd := m.launchCodex(kind)
			m2, ok := result.(Model)
			if !ok {
				t.Fatalf("launchCodex(%q) returned %T, want Model", kind, result)
			}
			if !strings.Contains(m2.toastMsg, "worktree setup failed") {
				t.Errorf("toastMsg = %q, want it to mention the worktree setup failure", m2.toastMsg)
			}
			if cmd != nil {
				t.Errorf("launchCodex(%q) returned a non-nil cmd, want the launch aborted", kind)
			}
		})
	}
}
