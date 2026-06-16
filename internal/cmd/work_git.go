package cmd

import (
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
