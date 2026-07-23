package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
)

// resolveWorktreePath computes the absolute path of the LEGACY single
// "additional" worktree for a project (kept for the legacy
// additional_worktree/worktree_path pair; the multi-slot pool resolves via
// Executor.ResolveWorktrees). Rules: an absolute worktree_path is used as-is; a
// "~"-prefixed one is home-expanded; a relative one resolves against the repo
// dir (so `../foo-additional` is a sibling); empty defaults to
// "<repo>-additional".
func resolveWorktreePath(proj *storage.Project, exec storage.Executor) string {
	return storage.ResolveWorktreeDir(proj.Path, exec.WorktreePath)
}

// resolveWorktreeBase applies the base-branch precedence for a reused
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

// prepareWorktree makes one "additional" worktree slot ready for a worker run:
// ensure it exists (reuse if present), seed untracked config files, keep the
// lock file out of git, then acquire the lock for (taskID, kind). On success it
// returns a release closure the caller MUST defer to free the lock (physical
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

// acquireWorktreeSlot claims one slot from the resolved worktree pool for
// (taskID, kind): with pin=0 the first slot whose lock is free (or stale) wins;
// pin=N (1-based) targets exactly that slot and fails fast when it is busy.
// The claimed slot is ensured + seeded + locked (prepareWorktree); the returned
// release closure MUST be deferred. When every slot is busy the error lists
// each slot's holder so the user can pick what to wait for or kill.
func acquireWorktreeSlot(proj *storage.Project, slots []storage.ResolvedWorktree, pin int, taskID, kind string) (storage.ResolvedWorktree, func(), error) {
	if len(slots) == 0 {
		return storage.ResolvedWorktree{}, nil, fmt.Errorf("no worktree slots configured - set `executor.worktrees` (or legacy `additional_worktree: true`) in project.yaml")
	}
	if pin > 0 {
		if pin > len(slots) {
			return storage.ResolvedWorktree{}, nil, fmt.Errorf("--slot %d out of range - project has %d worktree slot(s)", pin, len(slots))
		}
		slot := slots[pin-1]
		if holder := liveSlotHolder(slot.Path); holder != nil {
			return storage.ResolvedWorktree{}, nil, fmt.Errorf("slot %d (%s): %w", pin, slot.Path, &storage.WorktreeBusyError{Holder: holder})
		}
		release, err := prepareWorktree(proj, slot.Path, taskID, kind)
		if err != nil {
			return storage.ResolvedWorktree{}, nil, fmt.Errorf("slot %d (%s): %w", pin, slot.Path, err)
		}
		return slot, release, nil
	}

	var busy []string
	for i, slot := range slots {
		// Check the lock BEFORE touching the slot: ensure + config seeding must
		// never poke a worktree another run is live in.
		if holder := liveSlotHolder(slot.Path); holder != nil {
			busy = append(busy, fmt.Sprintf("slot %d (%s): pid %d, task %s", i+1, slot.Path, holder.PID, holder.TaskID))
			continue
		}
		release, err := prepareWorktree(proj, slot.Path, taskID, kind)
		if err == nil {
			return slot, release, nil
		}
		var be *storage.WorktreeBusyError
		if errors.As(err, &be) {
			// Lost the claim race to a run that started between the check and the
			// lock write - treat as busy and move on.
			busy = append(busy, fmt.Sprintf("slot %d (%s): pid %d, task %s", i+1, slot.Path, be.Holder.PID, be.Holder.TaskID))
			continue
		}
		// A non-busy failure (bad path, git refusing the worktree) is a config
		// problem, not contention - surface it instead of silently skipping.
		return storage.ResolvedWorktree{}, nil, fmt.Errorf("slot %d (%s): %w", i+1, slot.Path, err)
	}
	return storage.ResolvedWorktree{}, nil, fmt.Errorf("all %d worktree slot(s) busy - wait for a run to finish or kill one (K on the board):\n  %s",
		len(slots), strings.Join(busy, "\n  "))
}

// liveSlotHolder returns the lock holder when slotPath is held by another LIVE
// process, else nil (free, stale, or our own re-entrant lock).
func liveSlotHolder(slotPath string) *storage.WorktreeLock {
	lk, err := storage.ReadWorktreeLock(slotPath)
	if err != nil || lk == nil {
		return nil
	}
	if lk.PID == os.Getpid() || !storage.ProcessAlive(lk.PID) {
		return nil
	}
	return lk
}
