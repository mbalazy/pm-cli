package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// isGitRepo reports whether dir is inside a git work tree.
func isGitRepo(dir string) bool {
	c := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	out, err := c.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// gitDirty reports whether the working tree at dir has uncommitted changes.
func gitDirty(dir string) (bool, error) {
	c := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := c.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// requireCleanWorkingTree errors out if dir's git status could not be read
// (a failing `git status` must abort, never be misread as "clean") or if the
// tree has uncommitted changes. Shared precondition for the two places the
// executor switches branches in a checkout it does not own exclusively (the
// user's main checkout in standalone mode, the epic manager's non-worktree
// run) - guarding uncommitted work from a branch switch out from under it.
func requireCleanWorkingTree(dir string) error {
	dirty, err := gitDirty(dir)
	if err != nil {
		return fmt.Errorf("check working tree state at %s: %w", dir, err)
	}
	if dirty {
		return fmt.Errorf("working tree at %s is dirty - commit/stash first or pass --allow-dirty", dir)
	}
	return nil
}

// gitCheckoutBranch checks out branch at dir, creating it from the current HEAD
// if it does not yet exist.
func gitCheckoutBranch(dir, branch string) error {
	var c *exec.Cmd
	if branchExists(dir, branch) {
		c = exec.Command("git", "-C", dir, "checkout", branch)
	} else {
		c = exec.Command("git", "-C", dir, "checkout", "-b", branch)
	}
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

func branchExists(dir, branch string) bool {
	c := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return c.Run() == nil
}

// gitCurrentBranch returns the name of the currently checked-out branch.
func gitCurrentBranch(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitCleanWorktree brings dir to a pristine state WITHOUT changing the
// checked-out branch: it discards uncommitted changes to tracked files
// (reset --hard) and removes untracked NON-ignored files (clean -fd, no -x).
// Ignored files - node_modules, Pods, the copied .env/config files, the
// .pm-executor.lock - are deliberately preserved, so a reused "additional"
// worktree keeps its installed deps and configs between runs. Used before
// (re)starting work on the shared worktree so a killed run's leftovers never
// bleed into the next task's branch.
func gitCleanWorktree(dir string) error {
	if out, err := exec.Command("git", "-C", dir, "reset", "--hard").CombinedOutput(); err != nil {
		return fmt.Errorf("git reset --hard: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", dir, "clean", "-fd").CombinedOutput(); err != nil {
		return fmt.Errorf("git clean -fd: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// gitFreshBranch wipes the working tree clean (gitCleanWorktree) and then
// (re)creates branch at base - or at the current HEAD when base is empty/unknown
// - switching to it. `checkout -B` force-resets the branch even when it already
// exists, so a re-run after a kill starts fresh FROM BASE rather than continuing
// the previous partial branch. Ignored deps/configs survive. This is how a task
// or a sub gets its branch on the reused "additional" worktree.
func gitFreshBranch(dir, branch, base string) error {
	if err := gitCleanWorktree(dir); err != nil {
		return err
	}
	args := []string{"-C", dir, "checkout", "-B", branch}
	if base != "" && branchExists(dir, base) {
		args = append(args, base)
	}
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -B %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitEnsureBranch checks out branch, creating it from base (if base exists) or
// the current HEAD otherwise. Used by the manager to create the integration
// branch and per-sub feat branches off it.
func gitEnsureBranch(dir, branch, base string) error {
	var c *exec.Cmd
	switch {
	case branchExists(dir, branch):
		c = exec.Command("git", "-C", dir, "checkout", branch)
	case base != "" && branchExists(dir, base):
		c = exec.Command("git", "-C", dir, "checkout", "-b", branch, base)
	default:
		c = exec.Command("git", "-C", dir, "checkout", "-b", branch)
	}
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitMergeNoFF merges branch into the currently checked-out branch with a merge
// commit. On conflict (or any failure) it aborts the merge so the working tree
// is left clean, and returns the error.
func gitMergeNoFF(dir, branch, message string) error {
	c := exec.Command("git", "-C", dir, "merge", "--no-ff", "-m", message, branch)
	if out, err := c.CombinedOutput(); err != nil {
		// best-effort cleanup: leave the tree clean; the merge error is what matters
		_ = exec.Command("git", "-C", dir, "merge", "--abort").Run()
		return fmt.Errorf("merge %s failed (aborted): %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitDeleteBranch deletes a fully-merged branch.
func gitDeleteBranch(dir, branch string) error {
	if out, err := exec.Command("git", "-C", dir, "branch", "-d", branch).CombinedOutput(); err != nil {
		return fmt.Errorf("branch -d %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitAheadCount returns how many commits branch carries that base does not.
// Independent mode uses it to decide whether a sub's branch is worth pushing.
// The error is surfaced, never folded into 0: "could not count" (a typo'd
// base, a detached ref) must not read as "nothing to push" - that is exactly
// the partial work the push exists to save from the next branch wipe.
func gitAheadCount(dir, branch, base string) (int, error) {
	out, err := exec.Command("git", "-C", dir, "rev-list", "--count", base+".."+branch).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("rev-list %s..%s: %s", base, branch, strings.TrimSpace(string(out)))
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("rev-list %s..%s: unparseable count %q", base, branch, out)
	}
	return n, nil
}

// gitHasRemote reports whether the repo has at least one configured remote.
func gitHasRemote(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "remote").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// gitPushTimeout caps a push: every other long-running executor step (worker,
// prepare, baseline) has a deadline, and a wedged network here used to hang
// the whole manager - with the heartbeat already stopped, so it LOOKED hung
// too, just for the wrong reason.
const gitPushTimeout = 5 * time.Minute

// gitPush pushes branch to origin, setting upstream.
func gitPush(dir, branch string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitPushTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "git", "-C", dir, "push", "-u", "origin", branch)
	out, err := c.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("push %s timed out after %s", branch, gitPushTimeout)
	}
	if err != nil {
		return fmt.Errorf("push %s failed: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// quoteArgs renders argv for display, quoting args that contain whitespace or
// are multi-line so the dry-run command is copy-pasteable-ish.
func quoteArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n\"'") {
			if strings.Contains(a, "\n") {
				out[i] = "'<multi-line>'"
			} else {
				out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
			}
		} else {
			out[i] = a
		}
	}
	return out
}

// gitHeadSHA is the commit a worker is about to start from. Empty on any
// failure - every caller treats that as "unknown" and degrades rather than
// guessing.
func gitHeadSHA(dir string) string {
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
