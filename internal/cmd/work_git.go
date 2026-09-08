package cmd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
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

// gitRefExists reports whether ref resolves at dir - ANY ref, not just a local
// branch (branchExists only knows refs/heads). A slot forks from a
// remote-tracking ref (origin/<base>), which branchExists would deny.
func gitRefExists(dir, ref string) bool {
	c := exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", ref)
	return c.Run() == nil
}

// gitCheckoutDetached checks ref out with a DETACHED head. This is how a
// worktree slot reads the base branch: `git checkout <base>` is refused while
// another worktree of the same repo (the user's main checkout) holds that
// branch, and moving it with `branch -f` is refused for the same reason
// (pm-cli-131). Detaching takes no branch, so nothing is held and nothing moves.
func gitCheckoutDetached(dir, ref string) error {
	if out, err := exec.Command("git", "-C", dir, "checkout", "--detach", ref).CombinedOutput(); err != nil {
		return fmt.Errorf("checkout --detach %s: %s", ref, strings.TrimSpace(string(out)))
	}
	return nil
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
	// Any ref, not just a local branch: in a slot the base is origin/<base>.
	if base != "" && gitRefExists(dir, base) {
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

// baseFetchTimeout caps the fetch freshenBase does before a run forks from the
// base branch: a wedged network degrades to "run on what is local", never to a
// hung manager.
const baseFetchTimeout = 2 * time.Minute

// freshenBase brings the local base branch up to origin before anything forks
// from it. Retro pm-cli-119: independent-mode runs forked from a LOCAL
// `development` nothing had pulled - 10 commits behind origin on a 2026-08
// run, where a sub then "proved" the spec's code did not exist (a false
// SPEC-CONFLICT, re-run needed, ~$25); another sub found its base
// stale and rebased itself mid-task. Rules:
//   - no remote, base unknown, fetch fails or times out: nil, said on w - an
//     offline run is still a run, on the base it has;
//   - local base strictly BEHIND origin/<base>: fast-forward it (merge
//     --ff-only when it is checked out, branch -f otherwise) and say what moved;
//   - local base DIVERGED (commits on both sides): an error naming both SHAs and
//     the two ways out. Never pick one silently - the local commits may be
//     somebody's unpushed work.
func freshenBase(w io.Writer, dir, base string) error {
	if base == "" || !gitHasRemote(dir) || !branchExists(dir, base) {
		return nil
	}
	if !fetchBase(w, dir, base) {
		return nil
	}
	remote := "origin/" + base
	if !gitRefExists(dir, remote) {
		return nil // base has no counterpart on origin
	}
	behind, err := gitAheadCount(dir, remote, base) // origin commits the local base lacks
	if err != nil || behind == 0 {
		return nil
	}
	ahead, err := gitAheadCount(dir, base, remote) // local commits origin lacks
	if err != nil {
		return nil
	}
	localSHA, remoteSHA := gitShortSHA(dir, base), gitShortSHA(dir, remote)
	if ahead > 0 {
		return fmt.Errorf("base branch %s has diverged from %s (%d local commit(s) origin lacks, %d origin commit(s) missing locally; %s vs %s) - reconcile before the run: `git -C %s rebase %s %s` keeps the local commits, `git -C %s branch -f %s %s` drops them",
			base, remote, ahead, behind, localSHA, remoteSHA, dir, remote, base, dir, base, remote)
	}
	var c *exec.Cmd
	if cur, _ := gitCurrentBranch(dir); cur == base {
		c = exec.Command("git", "-C", dir, "merge", "--ff-only", "--quiet", remote)
	} else {
		c = exec.Command("git", "-C", dir, "branch", "-f", base, remote)
	}
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("fast-forward %s to %s (%d commit(s) behind): %s", base, remote, behind, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(w, "pm: fast-forwarded %s %s..%s (%d commit(s) behind origin)\n", base, localSHA, remoteSHA, behind)
	return nil
}

// fetchBase fetches origin/<base>, capped by baseFetchTimeout, and reports
// whether the fetch succeeded. A failure is said on w and is never fatal: an
// offline run is still a run, on the refs it has.
func fetchBase(w io.Writer, dir, base string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), baseFetchTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "fetch", "--quiet", "origin", base).CombinedOutput(); err != nil {
		fmt.Fprintf(w, "pm: could not fetch origin/%s (%s) - running on the local %s as is\n", base, strings.TrimSpace(string(out)), base)
		return false
	}
	return true
}

// slotForkRefName is the ref a run IN A WORKTREE SLOT forks from, resolved
// without touching the network: origin/<base> when it exists, the local base
// otherwise (no remote, or a base with no counterpart on origin). Side-effect
// free, so --dry-run reaches the same answer the run will.
func slotForkRefName(dir, base string) string {
	if base == "" || !gitHasRemote(dir) {
		return base
	}
	if remote := "origin/" + base; gitRefExists(dir, remote) {
		return remote
	}
	return base
}

// slotForkRef resolves the fork ref for a slot run after fetching origin.
//
// In a slot the base branch is a REF TO READ FROM, never a branch to check out
// or to move: git refuses both while the user's main checkout holds it
// (pm-cli-131). So there is no fast-forward here - the local base is simply not
// what the subs fork from - and local commits origin lacks are a WARNING (the
// run proceeds from origin), where the main-checkout path (freshenBase) still
// errors on a diverged base.
func slotForkRef(w io.Writer, dir, base string) string {
	if base == "" || !gitHasRemote(dir) {
		return base
	}
	if !fetchBase(w, dir, base) {
		return base
	}
	ref := slotForkRefName(dir, base)
	if ref == base {
		return base
	}
	if branchExists(dir, base) {
		if ahead, err := gitAheadCount(dir, base, ref); err == nil && ahead > 0 {
			fmt.Fprintf(w, "pm: the local %s carries %d commit(s) origin lacks - this slot run forks from %s (%s) and leaves the local branch alone\n",
				base, ahead, ref, gitShortSHA(dir, ref))
		}
	}
	return ref
}

// gitShortSHA is the abbreviated hash of ref, "?" when git cannot resolve it
// (only ever printed, never compared).
func gitShortSHA(dir, ref string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", ref).Output()
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(out))
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

// countUnreviewedCommits fills in how many commits the sub landed AFTER the last
// reviewer that actually ran, using the tip that reviewer could see
// (t.LastReviewedHead) and the sub's tip now.
//
// This is the machine-readable half of a state that used to live only in the
// worker's prose: a capped review loop cannot review its own last fix, because
// the round that would is the round past the cap. `blocked` says "something is
// unresolved"; this says "N commits of this branch were never adversarially
// reviewed", which is a different instruction to whoever picks the branch up.
//
// Silent no-op on every degraded path (no telemetry, no head recorded, a git
// that will not answer): the number is evidence, and a guessed one would be
// worse than none. A head that is no longer reachable - the worker rebased or
// amended its own history - is one of those paths: rev-list fails and the count
// stays 0 rather than becoming a fiction.
func countUnreviewedCommits(dir string, t *storage.ReviewTelemetry) {
	if t == nil || t.LastReviewedHead == "" || dir == "" {
		return
	}
	head := gitHeadSHA(dir)
	if head == "" || head == t.LastReviewedHead {
		return
	}
	c := exec.Command("git", "rev-list", "--count", t.LastReviewedHead+"..HEAD")
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n <= 0 {
		return
	}
	t.UnreviewedCommits = n
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
