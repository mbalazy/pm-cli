package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

// resolveWorktreePath computes the absolute path of the single "additional"
// worktree for a project. Rules: an absolute worktree_path is used as-is; a
// "~"-prefixed one is home-expanded; a relative one resolves against the repo
// dir (so `../foo-additional` is a sibling); empty defaults to
// "<repo>-additional". Only meaningful when exec.AdditionalWorktree is true.
func resolveWorktreePath(proj *storage.Project, exec storage.Executor) string {
	p := strings.TrimSpace(exec.WorktreePath)
	if p == "" {
		return filepath.Clean(proj.Path) + "-additional"
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(proj.Path, p))
}

// resolveWorktreeBase applies the base-branch precedence for the reused
// "additional" worktree: an explicit --base flag wins; else executor.base_branch
// from project.yaml; else the caller's fallback (pm work: the main checkout's
// current branch; pm run-epic: "main"). Keeps a forgotten --base from forking a
// task off whatever the user's main checkout happens to be on.
func resolveWorktreeBase(flagBase, execBase, fallback string) string {
	if strings.TrimSpace(flagBase) != "" {
		return flagBase
	}
	if strings.TrimSpace(execBase) != "" {
		return execBase
	}
	return fallback
}

// executorEnvSlice renders exec.Env as a sorted []string of KEY=VALUE pairs for
// injection into the worker's environment. Sorted purely for determinism. pm
// does not interpret these values.
func executorEnvSlice(exec storage.Executor) []string {
	if len(exec.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(exec.Env))
	for k := range exec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+exec.Env[k])
	}
	return out
}

// prepareWorktree makes the shared "additional" worktree ready for a worker
// run: ensure it exists (reuse if present), seed untracked config files, keep
// the lock file out of git, then acquire the lock for (taskID, kind). On success
// it returns a release closure the caller MUST defer to free the lock (physical
// worktree is intentionally left in place). A busy worktree returns
// *storage.WorktreeBusyError with a clear message and no release closure.
func prepareWorktree(proj *storage.Project, workDir, taskID, kind string) (func(), error) {
	if err := storage.EnsureWorktree(proj.Path, workDir); err != nil {
		return nil, err
	}
	// Best-effort seed of untracked config files (.env*, plists, ...). A failure
	// here only means the worker may miss a config; the worker (or the human) can
	// still recover, so it is not fatal.
	_ = storage.CopyUntrackedFiles(proj.Path, workDir)
	storage.ExcludeFromGit(proj.Path, ".pm-executor.lock")

	pid := os.Getpid()
	if err := storage.AcquireWorktreeLock(workDir, taskID, kind, pid); err != nil {
		return nil, err
	}
	return func() { _ = storage.ReleaseWorktreeLock(workDir, pid) }, nil
}
