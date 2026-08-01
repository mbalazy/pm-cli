package board

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
//
// Returns an error instead of swallowing it: a failed `git worktree add`
// (e.g. the branch is already checked out in another worktree) used to be
// dropped here, and the caller launched into the worktree anyway - one whose
// gitignored .env/plists were never seeded because CopyUntrackedFiles never
// ran either.
func copyWorktreeFiles(projDir, wtName string) error {
	wtPath := filepath.Join(projDir, ".claude", "worktrees", wtName)

	// Pre-create worktree if it doesn't exist yet
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		cmd := exec.Command("git", "worktree", "add", wtPath)
		cmd.Dir = projDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	return storage.CopyUntrackedFiles(projDir, wtPath, nil)
}
