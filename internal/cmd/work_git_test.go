package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// gitT runs a git command in dir, failing the test on error.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestGitFreshBranchOnReusedWorktree is the crux of the reused-worktree fix: a
// new task's branch must fork from the base branch's CURRENT tip and start from
// a clean tree - never inheriting a previous (killed) task's branch, its
// uncommitted changes, or leftover untracked source - while keeping ignored
// deps/configs (node_modules, .env).
func TestGitFreshBranchOnReusedWorktree(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "checkout", "-q", "-b", "development")
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n.env\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "base")

	wt := filepath.Join(t.TempDir(), "additional")
	if err := storage.EnsureWorktree(repo, wt); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// Simulate a previous task on the worktree + a kill aftermath:
	//  - its own feat branch with a commit,
	//  - an uncommitted change to a tracked file,
	//  - a leftover untracked source file,
	//  - installed deps + a copied config (both ignored, must survive).
	gitT(t, wt, "checkout", "-q", "-b", "feat/old")
	os.WriteFile(filepath.Join(wt, "app.txt"), []byte("old-committed\n"), 0644)
	gitT(t, wt, "commit", "-q", "-am", "old work")
	os.WriteFile(filepath.Join(wt, "app.txt"), []byte("DIRTY-uncommitted\n"), 0644)
	os.WriteFile(filepath.Join(wt, "leftover.go"), []byte("garbage\n"), 0644)
	os.MkdirAll(filepath.Join(wt, "node_modules"), 0755)
	os.WriteFile(filepath.Join(wt, "node_modules", "dep.js"), []byte("dep\n"), 0644)
	os.WriteFile(filepath.Join(wt, ".env"), []byte("SECRET=1\n"), 0644)

	// Base advances in the main checkout after the previous task started.
	os.WriteFile(filepath.Join(repo, "app.txt"), []byte("v2-base\n"), 0644)
	gitT(t, repo, "commit", "-q", "-am", "advance base")

	// New task: fresh branch off development.
	if err := gitFreshBranch(wt, "feat/new", "development"); err != nil {
		t.Fatalf("gitFreshBranch: %v", err)
	}

	if b, _ := gitCurrentBranch(wt); b != "feat/new" {
		t.Fatalf("expected to be on feat/new, got %q", b)
	}
	// Forked from development's CURRENT tip - not feat/old, not the dirty edit.
	if got, _ := os.ReadFile(filepath.Join(wt, "app.txt")); string(got) != "v2-base\n" {
		t.Fatalf("app.txt = %q; want base tip v2-base (old branch/dirt must be gone)", got)
	}
	// Leftover untracked source removed.
	if _, err := os.Stat(filepath.Join(wt, "leftover.go")); !os.IsNotExist(err) {
		t.Fatalf("leftover untracked file should have been cleaned")
	}
	// Ignored deps + configs preserved.
	if _, err := os.Stat(filepath.Join(wt, "node_modules", "dep.js")); err != nil {
		t.Fatalf("node_modules must be preserved across a fresh branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".env")); err != nil {
		t.Fatalf(".env config must be preserved across a fresh branch: %v", err)
	}
}

// TestGitCleanWorktreePreservesCommitsAndIgnored verifies the standalone clean
// used before touching an epic's integration branch: it drops the dirty tree but
// keeps committed history and ignored files, without switching branches.
func TestGitCleanWorktreePreservesCommitsAndIgnored(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "checkout", "-q", "-b", "epic/x")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("committed\n"), 0644)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "epic work")

	// Dirty state + leftover + ignored file.
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("dirty\n"), 0644)
	os.WriteFile(filepath.Join(repo, "junk.txt"), []byte("junk\n"), 0644)
	os.WriteFile(filepath.Join(repo, ".env"), []byte("K=V\n"), 0644)

	if err := gitCleanWorktree(repo); err != nil {
		t.Fatalf("gitCleanWorktree: %v", err)
	}

	// Still on epic/x, commit intact, dirt reverted, junk gone, .env kept.
	if b, _ := gitCurrentBranch(repo); b != "epic/x" {
		t.Fatalf("branch changed to %q; clean must not switch branches", b)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(got) != "committed\n" {
		t.Fatalf("a.txt = %q; want reverted to committed", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "junk.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked junk should be cleaned")
	}
	if _, err := os.Stat(filepath.Join(repo, ".env")); err != nil {
		t.Fatalf(".env must be preserved: %v", err)
	}
}

// TestGitAheadCountAndPushIfAhead covers the independent-mode push gate: a sub
// branch is pushed only when it carries commits its base does not, and a repo
// without a remote reports that instead of failing the run.
func TestGitAheadCountAndPushIfAhead(t *testing.T) {
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "checkout", "-q", "-b", "development")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "base")

	gitT(t, repo, "checkout", "-q", "-b", "feat/empty", "development")
	if n := gitAheadCount(repo, "feat/empty", "development"); n != 0 {
		t.Errorf("gitAheadCount(empty branch) = %d, want 0", n)
	}
	if note := pushIfAhead(repo, "feat/empty", "development"); note != "" {
		t.Errorf("pushIfAhead(empty branch) = %q, want no-op", note)
	}

	gitT(t, repo, "checkout", "-q", "-b", "feat/work", "development")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("work\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "work 1")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("work 2\n"), 0644)
	gitT(t, repo, "commit", "-q", "-am", "work 2")
	if n := gitAheadCount(repo, "feat/work", "development"); n != 2 {
		t.Errorf("gitAheadCount(2 commits) = %d, want 2", n)
	}
	if note := pushIfAhead(repo, "feat/work", "development"); !strings.Contains(note, "no remote") {
		t.Errorf("pushIfAhead without remote = %q, want a 'no remote' note", note)
	}

	// With a (local bare) remote the push succeeds and reports the branch.
	bare := t.TempDir()
	gitT(t, bare, "init", "-q", "--bare")
	gitT(t, repo, "remote", "add", "origin", bare)
	if note := pushIfAhead(repo, "feat/work", "development"); note != "pushed feat/work" {
		t.Errorf("pushIfAhead with remote = %q, want 'pushed feat/work'", note)
	}
	out, err := exec.Command("git", "-C", bare, "rev-parse", "--verify", "refs/heads/feat/work").Output()
	if err != nil || len(out) == 0 {
		t.Errorf("feat/work not present on the remote after push: %v", err)
	}

	if n := gitAheadCount(repo, "feat/work", "missing-base"); n != 0 {
		t.Errorf("gitAheadCount with unknown base = %d, want 0 (error swallowed)", n)
	}
}
