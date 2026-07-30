package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// worktreeLockFile is the on-disk lock claiming the shared "additional"
// worktree, written at the worktree root.
const worktreeLockFile = ".pm-executor.lock"

// DefaultSeedExcludes are the untracked directories the worktree seeding skips
// by default: dependency/build artifacts that are huge (a JS repo carries
// ~100k node_modules/Pods files), regenerable in place, and actively DANGEROUS
// to copy - the file-by-file copy turns symlinks (e.g. node_modules/.bin/*)
// into broken plain files. Seeding is for config files a build needs (.env*,
// plists), never for artifacts. Overridable per project via
// executor.seed_exclude.
var DefaultSeedExcludes = []string{
	"node_modules/", "Pods/", "DerivedData/", ".gradle/",
	".venv/", "venv/", "__pycache__/",
	"target/", "build/", "dist/",
}

// SeedExcludes returns the project's seeding exclude list: executor.seed_exclude
// when set (REPLACES the defaults), else DefaultSeedExcludes.
func (e Executor) SeedExcludes() []string {
	if len(e.SeedExclude) > 0 {
		return e.SeedExclude
	}
	return DefaultSeedExcludes
}

// seedExcluded reports whether rel (a slash-separated repo-relative path) falls
// under any exclude entry. An entry names a directory (trailing "/" optional)
// and matches it at ANY depth, so "node_modules/" covers nested workspace
// installs and "Pods/" covers "ios/Pods/".
func seedExcluded(rel string, excludes []string) bool {
	for _, e := range excludes {
		e = strings.TrimSuffix(strings.TrimSpace(e), "/")
		if e == "" {
			continue
		}
		if strings.HasPrefix(rel, e+"/") || strings.Contains(rel, "/"+e+"/") {
			return true
		}
	}
	return false
}

// CopyUntrackedFiles copies untracked files (INCLUDING gitignored ones -
// `git ls-files --others`, no --exclude-standard) from srcRepo into dstDir,
// skipping any file that already exists at the destination and any path under
// excludes (nil = DefaultSeedExcludes). This is how the executor and the TUI
// seed a fresh worktree with the untracked config files a build needs (.env*,
// GoogleService-Info.plist, .xcode.env.local, ...) that are deliberately kept
// out of git - dependency artifacts are excluded and get installed in place
// instead (see executor.prepare). Existing files are never clobbered, so it is
// safe to re-run against a persistent worktree. Symlinks are recreated as
// symlinks, never dereferenced into copies.
func CopyUntrackedFiles(srcRepo, dstDir string, excludes []string) error {
	if excludes == nil {
		excludes = DefaultSeedExcludes
	}
	cmd := exec.Command("git", "-C", srcRepo, "ls-files", "--others")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git ls-files --others in %s: %w", srcRepo, err)
	}
	for _, rel := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if rel == "" || seedExcluded(rel, excludes) {
			continue
		}
		src := filepath.Join(srcRepo, rel)
		dst := filepath.Join(dstDir, rel)
		if _, err := os.Lstat(dst); err == nil {
			continue // never clobber an existing destination file
		}
		info, err := os.Lstat(src)
		if err != nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0755)
		if info.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(src); err == nil {
				_ = os.Symlink(target, dst)
			}
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		_ = os.WriteFile(dst, data, info.Mode())
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

// IsGitWorktree reports whether dir is a usable git working tree - the same
// test EnsureWorktree uses to decide between reusing a slot and refusing it, so
// `pm executor doctor` can predict that refusal instead of waiting for a run to
// hit it.
func IsGitWorktree(dir string) bool { return isInsideWorkTree(dir) }

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

// ResolvedWorktree is one usable worktree slot after config resolution: an
// absolute path plus the fully merged KEY=VALUE env for processes running
// against it.
type ResolvedWorktree struct {
	Path string
	Env  []string
}

// ResolveWorktrees returns the executor's worktree slot pool resolved against
// the project's repo path, in config order. The `worktrees` list supersedes the
// legacy pair; a legacy `additional_worktree: true` (with optional
// worktree_path/env) synthesizes a single slot so old configs keep working.
// Empty -> nil (worktrees not configured). Slot env = executor-level Env
// overlaid with the slot's own Env (slot wins), sorted for determinism.
func (e Executor) ResolveWorktrees(projPath string) []ResolvedWorktree {
	if len(e.Worktrees) > 0 {
		out := make([]ResolvedWorktree, 0, len(e.Worktrees))
		for i, s := range e.Worktrees {
			out = append(out, ResolvedWorktree{
				Path: resolveSlotDir(projPath, s.Path, i),
				Env:  mergedEnvSlice(e.Env, s.Env),
			})
		}
		return out
	}
	if e.AdditionalWorktree {
		return []ResolvedWorktree{{Path: ResolveWorktreeDir(projPath, e.WorktreePath), Env: e.EnvSlice()}}
	}
	return nil
}

// ResolveWorktreeDir resolves a configured worktree path against the repo dir:
// absolute is used as-is, "~" is home-expanded, relative resolves against
// projPath (so `../foo-additional` is a sibling), empty defaults to
// "<repo>-additional".
func ResolveWorktreeDir(projPath, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return filepath.Clean(projPath) + "-additional"
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(projPath, p))
}

// resolveSlotDir is ResolveWorktreeDir with an index-aware default, so two
// pathless slots never collide: slot 1 defaults to "<repo>-additional", slot
// N>1 to "<repo>-additional-N".
func resolveSlotDir(projPath, p string, idx int) string {
	if strings.TrimSpace(p) == "" && idx > 0 {
		return fmt.Sprintf("%s-additional-%d", filepath.Clean(projPath), idx+1)
	}
	return ResolveWorktreeDir(projPath, p)
}

// mergedEnvSlice overlays over onto base (over wins) and renders the result as
// a sorted []string of KEY=VALUE pairs. Nil when both maps are empty.
func mergedEnvSlice(base, over map[string]string) []string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	m := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		m[k] = v
	}
	for k, v := range over {
		m[k] = v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
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

// LiveWorktreeHolder returns the lock holder when worktreePath is held by
// another LIVE process, else nil (free, stale, or the caller's own re-entrant
// lock). Shared by the slot allocator (cmd) and the board's slot indicator.
func LiveWorktreeHolder(worktreePath string) *WorktreeLock {
	lk, err := ReadWorktreeLock(worktreePath)
	if err != nil || lk == nil {
		return nil
	}
	if lk.PID == os.Getpid() || !ProcessAlive(lk.PID) {
		return nil
	}
	return lk
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
//
// The claim itself is ATOMIC, via two primitives:
//   - claim = link(2) of a fully-written private tmp file onto the lock path -
//     the lock either doesn't exist or exists WITH complete content, so a rival
//     can never observe a half-written winner (a plain O_EXCL create + write
//     has exactly that window). Two simultaneous links: one wins, the loser
//     gets EEXIST, reads the winner's payload and reports busy.
//   - takeover of a stale/corrupt lock = rename it aside, then re-claim. Two
//     simultaneous takeovers race on the rename; the loser's rename fails
//     (ENOENT) and its next claim attempt sees the winner's fresh lock.
func AcquireWorktreeLock(worktreePath, taskID, kind string, pid int) error {
	lk := WorktreeLock{PID: pid, TaskID: taskID, Kind: kind, Started: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(&lk, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		return err
	}
	path := worktreeLockPath(worktreePath)

	for attempt := 0; attempt < 5; attempt++ {
		tmp := fmt.Sprintf("%s.%d.tmp", path, pid)
		if err := os.WriteFile(tmp, data, 0644); err != nil {
			return err
		}
		linkErr := os.Link(tmp, path)
		_ = os.Remove(tmp)
		if linkErr == nil {
			return nil
		}
		if !os.IsExist(linkErr) {
			return linkErr
		}

		existing, rerr := ReadWorktreeLock(worktreePath)
		if rerr == nil {
			if existing == nil {
				continue // vanished between EEXIST and read (released/stolen) - retry
			}
			if existing.PID == pid {
				// Our own re-entrant lock: refresh the payload (the task id changes
				// as the epic manager re-enters per sub). Only this process WRITES a
				// lock it owns, but others READ it concurrently (the board's slot
				// indicator, a rival's busy check) - an in-place write here has a
				// truncated-file window, and a rival reading that torn state as
				// corrupt would steal a LIVE holder's lock. Write-tmp + rename keeps
				// the refresh atomic like every other lock transition.
				refresh := fmt.Sprintf("%s.%d.refresh", path, pid)
				if err := os.WriteFile(refresh, data, 0644); err != nil {
					return err
				}
				return os.Rename(refresh, path)
			}
			if ProcessAlive(existing.PID) {
				return &WorktreeBusyError{Holder: existing}
			}
		}
		// Stale (dead holder) or corrupt (rerr != nil - a truncated file from a
		// crashed process must not brick the slot). Steal it ATOMICALLY: rename
		// aside and loop to re-claim. If a rival steals first our rename fails
		// harmlessly and the next attempt sees their fresh lock.
		steal := fmt.Sprintf("%s.steal.%d", path, pid)
		if os.Rename(path, steal) == nil {
			_ = os.Remove(steal)
		}
	}
	return fmt.Errorf("could not acquire worktree lock at %s (takeover contention)", path)
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
