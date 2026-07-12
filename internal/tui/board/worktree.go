package board

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mbalazy/pm/internal/storage"
)

// worktreeName returns a semantic name for the worktree branch.
// Uses task branch if set, otherwise slugifies the task title.
func worktreeName(t *storage.Task) string {
	if t.Meta.Branch != "" {
		return t.Meta.Branch
	}
	return storage.Slugify(t.Meta.Title)
}

// copyWorktreeFiles pre-creates a git worktree (if needed) and copies all
// untracked files from the main repo into the worktree. The copy itself is
// shared with the executor via storage.CopyUntrackedFiles.
func copyWorktreeFiles(projDir, wtName string) {
	wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)

	// Pre-create worktree if it doesn't exist yet
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		cmd := exec.Command("git", "worktree", "add", wtPath)
		cmd.Dir = projDir
		if err := cmd.Run(); err != nil {
			return
		}
	}

	_ = storage.CopyUntrackedFiles(projDir, wtPath)
}
