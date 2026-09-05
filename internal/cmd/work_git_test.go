package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// gitT runs a git command in dir, failing the test on error. The identity is
// injected per-command, which covers gitT's OWN commits but nothing else - see
// gitInitRepo for why that is not enough.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitInitRepo creates a fixture repo and writes the test identity into the
// repo's OWN config. Use it instead of a bare `gitInitRepo(t, dir)`.
//
// gitT's environment reaches only the commands gitT itself runs. Most commits
// in a fixture repo are made by somebody else: the code under test shelling out
// to git, or the fake `claude` worker's script. Those inherit the TEST PROCESS
// environment, where the identity comes from the developer's global gitconfig -
// which a CI runner does not have. There git derives one from the OS account,
// whose name field is empty, and refuses: "fatal: empty ident name not allowed".
//
// The failure mode is silent and CI-only: the commit never happens, and only a
// test that ASSERTS on that commit goes red - on the runner, never on a laptop.
// Repo-level config is what travels with the directory into every subprocess,
// so it is the only fix that covers all three writers.
func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "config", "user.email", "t@t")
	gitT(t, dir, "config", "user.name", "t")
}

// fakeGitFailing installs a fake `git` at the front of PATH that fails with
// stderr text when the invocation's argv (space-joined) ends with failSuffix
// (which must include its own leading space, e.g. " status --porcelain"),
// and otherwise execs the real git binary unchanged. Used to force an error
// out of a specific git subcommand without corrupting a real repo.
func fakeGitFailing(t *testing.T, failSuffix, stderr string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("no real git on PATH: %v", err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"" + failSuffix + "\")\n" +
		"    echo '" + stderr + "' >&2\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n" +
		"exec " + realGit + " \"$@\"\n"
	path := filepath.Join(dir, "git")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGitFreshBranchOnReusedWorktree is the crux of the reused-worktree fix: a
// new task's branch must fork from the base branch's CURRENT tip and start from
// a clean tree - never inheriting a previous (killed) task's branch, its
// uncommitted changes, or leftover untracked source - while keeping ignored
// deps/configs (node_modules, .env).
func TestGitFreshBranchOnReusedWorktree(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
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
	gitInitRepo(t, repo)
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
// freshenBase (pm-cli-119-7): the local base is brought up to origin before a
// run forks from it - fast-forward when strictly behind (both the checked-out
// and the not-checked-out case), a loud error when diverged, silence when
// there is nothing to do or no remote at all.
func TestFreshenBase(t *testing.T) {
	// origin (bare) <- A (the run's checkout) and B (somebody else's push).
	origin := t.TempDir()
	gitT(t, origin, "init", "-q", "--bare")
	clone := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "clone")
		gitT(t, t.TempDir(), "clone", "-q", origin, dir)
		gitT(t, dir, "config", "user.email", "t@t")
		gitT(t, dir, "config", "user.name", "t")
		return dir
	}
	commit := func(t *testing.T, dir, name string) {
		writeFile(t, filepath.Join(dir, name), name)
		gitT(t, dir, "add", name)
		gitT(t, dir, "commit", "-q", "-m", name)
	}
	a := clone(t)
	gitT(t, a, "checkout", "-q", "-b", "main")
	commit(t, a, "one")
	gitT(t, a, "push", "-q", "-u", "origin", "main")
	b := clone(t)
	gitT(t, b, "checkout", "-q", "main")
	gitT(t, b, "config", "user.email", "t@t")
	gitT(t, b, "config", "user.name", "t")

	sha := func(dir, ref string) string { return gitShortSHA(dir, ref) }

	t.Run("up to date -> nothing said, nothing moved", func(t *testing.T) {
		var out bytes.Buffer
		before := sha(a, "main")
		if err := freshenBase(&out, a, "main"); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 || sha(a, "main") != before {
			t.Errorf("nothing should happen when local == origin, got %q, main %s->%s", out.String(), before, sha(a, "main"))
		}
	})

	t.Run("behind, checked out -> fast-forwarded", func(t *testing.T) {
		commit(t, b, "two")
		gitT(t, b, "push", "-q", "origin", "main")
		var out bytes.Buffer
		if err := freshenBase(&out, a, "main"); err != nil {
			t.Fatal(err)
		}
		if sha(a, "main") != sha(b, "main") {
			t.Errorf("main not fast-forwarded: %s vs origin %s", sha(a, "main"), sha(b, "main"))
		}
		if !strings.Contains(out.String(), "fast-forwarded main") || !strings.Contains(out.String(), "1 commit(s) behind") {
			t.Errorf("the move must be said: %q", out.String())
		}
	})

	t.Run("behind, NOT checked out -> branch moved without touching the tree", func(t *testing.T) {
		gitT(t, a, "checkout", "-q", "-b", "elsewhere")
		commit(t, b, "three")
		gitT(t, b, "push", "-q", "origin", "main")
		var out bytes.Buffer
		if err := freshenBase(&out, a, "main"); err != nil {
			t.Fatal(err)
		}
		if sha(a, "main") != sha(b, "main") {
			t.Errorf("main not moved: %s vs origin %s", sha(a, "main"), sha(b, "main"))
		}
		if cur, _ := gitCurrentBranch(a); cur != "elsewhere" {
			t.Errorf("the checked-out branch must stay put, now on %q", cur)
		}
		gitT(t, a, "checkout", "-q", "main")
	})

	t.Run("diverged -> error naming both SHAs, nothing moved", func(t *testing.T) {
		commit(t, a, "local-only")
		commit(t, b, "four")
		gitT(t, b, "push", "-q", "origin", "main")
		before := sha(a, "main")
		var out bytes.Buffer
		err := freshenBase(&out, a, "main")
		if err == nil {
			t.Fatal("a diverged base must abort the run, not be resolved silently")
		}
		for _, want := range []string{"diverged", before, sha(b, "main"), "rebase", "branch -f"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should carry %q: %v", want, err)
			}
		}
		if sha(a, "main") != before {
			t.Errorf("a diverged base must not be moved: %s -> %s", before, sha(a, "main"))
		}
	})

	t.Run("no remote -> silent no-op", func(t *testing.T) {
		dir := t.TempDir()
		gitInitRepo(t, dir)
		gitT(t, dir, "checkout", "-q", "-b", "main")
		commit(t, dir, "solo")
		var out bytes.Buffer
		if err := freshenBase(&out, dir, "main"); err != nil || out.Len() != 0 {
			t.Errorf("no remote: want silence, got err=%v out=%q", err, out.String())
		}
	})
}

func TestGitAheadCountAndPushIfAhead(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	gitT(t, repo, "checkout", "-q", "-b", "development")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "base")

	gitT(t, repo, "checkout", "-q", "-b", "feat/empty", "development")
	if n, err := gitAheadCount(repo, "feat/empty", "development"); n != 0 || err != nil {
		t.Errorf("gitAheadCount(empty branch) = %d, %v, want 0, nil", n, err)
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
	if n, err := gitAheadCount(repo, "feat/work", "development"); n != 2 || err != nil {
		t.Errorf("gitAheadCount(2 commits) = %d, %v, want 2, nil", n, err)
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

	// A broken ahead-check (typo'd base) must SURFACE the error - and
	// pushIfAhead must respond by pushing anyway rather than silently
	// dropping the work as "0 commits ahead".
	if _, err := gitAheadCount(repo, "feat/work", "missing-base"); err == nil {
		t.Error("gitAheadCount with unknown base must return an error, not a silent 0")
	}
	note := pushIfAhead(repo, "feat/work", "missing-base")
	if !strings.Contains(note, "pushed feat/work") || !strings.Contains(note, "ahead-check failed") {
		t.Errorf("pushIfAhead with a broken ahead-check = %q, want push-to-be-safe with the reason", note)
	}
}

// TestGitThinWrappersSurfaceStderr covers the three wrappers that used to
// return a bare `*ExitError` ("exit status N") on failure - gitCheckoutBranch,
// gitEnsureBranch, gitDeleteBranch - with real git failures, verifying the
// returned error carries git's own stderr text, not just the exit code.
func TestGitThinWrappersSurfaceStderr(t *testing.T) {
	t.Run("gitCheckoutBranch: invalid branch name", func(t *testing.T) {
		repo := t.TempDir()
		gitInitRepo(t, repo)
		gitT(t, repo, "checkout", "-q", "-b", "main")
		os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
		gitT(t, repo, "add", ".")
		gitT(t, repo, "commit", "-q", "-m", "init")

		err := gitCheckoutBranch(repo, "bad..name")
		if err == nil {
			t.Fatal("expected an error for an invalid branch name")
		}
		if err.Error() == "exit status 128" || !strings.Contains(err.Error(), "not a valid branch name") {
			t.Fatalf("error must carry git's stderr text, got: %v", err)
		}
	})

	t.Run("gitEnsureBranch: checkout blocked by conflicting local changes", func(t *testing.T) {
		repo := t.TempDir()
		gitInitRepo(t, repo)
		gitT(t, repo, "checkout", "-q", "-b", "main")
		os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
		gitT(t, repo, "add", ".")
		gitT(t, repo, "commit", "-q", "-m", "init")
		gitT(t, repo, "checkout", "-q", "-b", "other")
		os.WriteFile(filepath.Join(repo, "f.txt"), []byte("other\n"), 0644)
		gitT(t, repo, "commit", "-q", "-am", "other-commit")
		gitT(t, repo, "checkout", "-q", "main")
		os.WriteFile(filepath.Join(repo, "f.txt"), []byte("conflict\n"), 0644)

		err := gitEnsureBranch(repo, "other", "")
		if err == nil {
			t.Fatal("expected an error - local changes would be overwritten")
		}
		if !strings.Contains(err.Error(), "overwritten by checkout") {
			t.Fatalf("error must carry git's stderr text, got: %v", err)
		}
	})

	t.Run("gitDeleteBranch: branch not fully merged", func(t *testing.T) {
		repo := t.TempDir()
		gitInitRepo(t, repo)
		gitT(t, repo, "checkout", "-q", "-b", "main")
		os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
		gitT(t, repo, "add", ".")
		gitT(t, repo, "commit", "-q", "-m", "init")
		gitT(t, repo, "checkout", "-q", "-b", "unmerged")
		os.WriteFile(filepath.Join(repo, "g.txt"), []byte("y\n"), 0644)
		gitT(t, repo, "add", ".")
		gitT(t, repo, "commit", "-q", "-m", "wip")
		gitT(t, repo, "checkout", "-q", "main")

		err := gitDeleteBranch(repo, "unmerged")
		if err == nil {
			t.Fatal("expected an error - branch is not fully merged")
		}
		if !strings.Contains(err.Error(), "not fully merged") {
			t.Fatalf("error must carry git's stderr text, got: %v", err)
		}
	})
}

// TestRequireCleanWorkingTreePropagatesStatusError verifies the shared
// clean-tree precondition (used by both pm work standalone and pm run-epic's
// non-worktree path) surfaces a failing `git status` as an error instead of
// silently treating it as a clean tree - the bug was `if dirty, _ :=
// gitDirty(dir); dirty` discarding the error, so a broken git status let a
// dirty (or unreadable) tree slip past the guard that exists specifically to
// protect uncommitted work from a branch switch.
func TestRequireCleanWorkingTreePropagatesStatusError(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	gitT(t, repo, "checkout", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	fakeGitFailing(t, " status --porcelain", "fatal: fake status failure")
	if err := requireCleanWorkingTree(repo); err == nil {
		t.Fatal("expected requireCleanWorkingTree to surface the git status failure, got nil")
	}
}

// TestRequireCleanWorkingTreeCleanAndDirty are the (pre-existing) happy-path
// cases, kept alongside the error-propagation test above for contrast.
func TestRequireCleanWorkingTreeCleanAndDirty(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	gitT(t, repo, "checkout", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	if err := requireCleanWorkingTree(repo); err != nil {
		t.Fatalf("clean tree must pass: %v", err)
	}

	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("dirty\n"), 0644)
	err := requireCleanWorkingTree(repo)
	if err == nil || !strings.Contains(err.Error(), "is dirty") {
		t.Fatalf("dirty tree must be rejected with a clear message, got: %v", err)
	}
}
