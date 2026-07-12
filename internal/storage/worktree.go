package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// worktreeLockFile is the on-disk lock claiming the shared "additional"
// worktree, written at the worktree root.
const worktreeLockFile = ".pm-executor.lock"

// CopyUntrackedFiles copies every untracked file (INCLUDING gitignored ones -
// `git ls-files --others`, no --exclude-standard) from srcRepo into dstDir,
// skipping any file that already exists at the destination. This is how the
// executor and the TUI seed a fresh worktree with the untracked config files a
// build needs (.env*, GoogleService-Info.plist, .xcode.env.local, ...) that are
// deliberately kept out of git. Existing files are never clobbered, so it is
// safe to re-run against a persistent worktree.
func CopyUntrackedFiles(srcRepo, dstDir string) error {
	cmd := exec.Command("git", "-C", srcRepo, "ls-files", "--others")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git ls-files --others in %s: %w", srcRepo, err)
	}
	for _, rel := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if rel == "" {
			continue
		}
		src := filepath.Join(srcRepo, rel)
		dst := filepath.Join(dstDir, rel)
		if _, err := os.Stat(dst); err == nil {
			continue // never clobber an existing destination file
		}
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		mode := os.FileMode(0644)
		if info, err := os.Stat(src); err == nil {
			mode = info.Mode()
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0755)
		_ = os.WriteFile(dst, data, mode)
	}
	return nil
}

// EnsureWorktree makes sure worktreePath is a usable git worktree of mainRepo,
// creating it (detached at HEAD) only when it does not exist yet. An existing
// worktree is REUSED as-is so a prior run's installed dependencies
// (node_modules, Pods, ...) survive between runs. A path that exists but is not
// a git worktree is refused rather than clobbered.
func EnsureWorktree(mainRepo, worktreePath string) error {
	if isInsideWorkTree(worktreePath) {
		return nil // already a worktree - reuse
	}
	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree path %s exists but is not a git worktree - remove it or run `git worktree prune`", worktreePath)
	}
	cmd := exec.Command("git", "-C", mainRepo, "worktree", "add", "--detach", worktreePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add %s: %s", worktreePath, strings.TrimSpace(string(out)))
	}
	return nil
}

func isInsideWorkTree(dir string) bool {
	c := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := c.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// ExcludeFromGit best-effort adds pattern to the repo's shared git exclude
// (.git/info/exclude) so the executor lock file never shows up in `git status`
// or gets swept into a worker's `git add`. Failures are ignored - the lock still
// works, it is just cosmetically visible.
func ExcludeFromGit(repo, pattern string) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return
	}
	commonDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo, commonDir)
	}
	excludePath := filepath.Join(commonDir, "info", "exclude")
	if data, err := os.ReadFile(excludePath); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(ln) == pattern {
				return // already excluded
			}
		}
	}
	_ = os.MkdirAll(filepath.Dir(excludePath), 0755)
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(pattern + "\n")
}

// WorktreeLock is the JSON payload of a worktree lock: who holds the shared
// "additional" worktree right now.
type WorktreeLock struct {
	PID     int    `json:"pid"`
	TaskID  string `json:"task_id"`
	Kind    string `json:"kind"`    // "work" | "run-epic"
	Started string `json:"started"` // RFC3339
}

// WorktreeBusyError is returned by AcquireWorktreeLock when the worktree is held
// by another LIVE process.
type WorktreeBusyError struct {
	Holder *WorktreeLock
}

func (e *WorktreeBusyError) Error() string {
	kind := e.Holder.Kind
	if kind == "" {
		kind = "a run"
	}
	return fmt.Sprintf("additional worktree busy (pid %d, task %s) - %s is running there; wait for it or kill it (K on the board)",
		e.Holder.PID, e.Holder.TaskID, kind)
}

func worktreeLockPath(worktreePath string) string {
	return filepath.Join(worktreePath, worktreeLockFile)
}

// ReadWorktreeLock returns the lock currently held on worktreePath, or (nil,
// nil) when there is none.
func ReadWorktreeLock(worktreePath string) (*WorktreeLock, error) {
	data, err := os.ReadFile(worktreeLockPath(worktreePath))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lk WorktreeLock
	if err := json.Unmarshal(data, &lk); err != nil {
		return nil, err
	}
	return &lk, nil
}

// AcquireWorktreeLock claims worktreePath for (pid, taskID, kind). A lock held
// by another LIVE process returns *WorktreeBusyError. A stale lock (the holder's
// pid is dead) is taken over automatically. Re-acquiring with the same pid is
// idempotent (lets the epic manager re-enter its own lock per sub).
func AcquireWorktreeLock(worktreePath, taskID, kind string, pid int) error {
	existing, err := ReadWorktreeLock(worktreePath)
	if err != nil {
		return err
	}
	if existing != nil && existing.PID != pid && ProcessAlive(existing.PID) {
		return &WorktreeBusyError{Holder: existing}
	}
	// Free, stale (dead holder), or our own lock -> claim it.
	lk := WorktreeLock{PID: pid, TaskID: taskID, Kind: kind, Started: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(&lk, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		return err
	}
	return os.WriteFile(worktreeLockPath(worktreePath), data, 0644)
}

// ReleaseWorktreeLock removes the lock on worktreePath, but only when it is held
// by pid (so a process never steals a lock now owned by someone else). A missing
// lock, or one held by another pid, is a no-op. Idempotent.
func ReleaseWorktreeLock(worktreePath string, pid int) error {
	existing, err := ReadWorktreeLock(worktreePath)
	if err != nil || existing == nil {
		return err
	}
	if existing.PID != pid {
		return nil
	}
	err = os.Remove(worktreeLockPath(worktreePath))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
