package cmd

import (
	"fmt"
	"os/exec"
	"strings"
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

// gitCheckoutBranch checks out branch at dir, creating it from the current HEAD
// if it does not yet exist.
func gitCheckoutBranch(dir, branch string) error {
	if branchExists(dir, branch) {
		return exec.Command("git", "-C", dir, "checkout", branch).Run()
	}
	return exec.Command("git", "-C", dir, "checkout", "-b", branch).Run()
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

// gitEnsureBranch checks out branch, creating it from base (if base exists) or
// the current HEAD otherwise. Used by the manager to create the integration
// branch and per-sub feat branches off it.
func gitEnsureBranch(dir, branch, base string) error {
	if branchExists(dir, branch) {
		return exec.Command("git", "-C", dir, "checkout", branch).Run()
	}
	if base != "" && branchExists(dir, base) {
		return exec.Command("git", "-C", dir, "checkout", "-b", branch, base).Run()
	}
	return exec.Command("git", "-C", dir, "checkout", "-b", branch).Run()
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
	return exec.Command("git", "-C", dir, "branch", "-d", branch).Run()
}

// gitHasRemote reports whether the repo has at least one configured remote.
func gitHasRemote(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "remote").Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// gitPush pushes branch to origin, setting upstream.
func gitPush(dir, branch string) error {
	c := exec.Command("git", "-C", dir, "push", "-u", "origin", branch)
	if out, err := c.CombinedOutput(); err != nil {
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
