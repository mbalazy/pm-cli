package board

import (
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
// untracked files from the main repo into the worktree.
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

	// Find all untracked files (both ignored and non-ignored)
	cmd := exec.Command("git", "ls-files", "--others")
	cmd.Dir = projDir
	out, err := cmd.Output()
	if err != nil {
		return
	}

	for _, rel := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if rel == "" {
			continue
		}
		src := filepath.Join(projDir, rel)
		dst := filepath.Join(wtPath, rel)

		// Skip if destination already exists
		if _, err := os.Stat(dst); err == nil {
			continue
		}

		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		info, _ := os.Stat(src)
		os.MkdirAll(filepath.Dir(dst), 0755)
		os.WriteFile(dst, data, info.Mode())
	}
}
