package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// slotFixture builds the shape pm-cli-131 was reported on: a bare `origin`, a
// main checkout sitting ON the base branch, and an executor worktree slot of
// that same clone. It returns the main checkout, the slot and the bare repo.
func slotFixture(t *testing.T) (main, slot, origin string) {
	t.Helper()
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	gitT(t, root, "init", "--bare", "-q", "--initial-branch=main", origin)

	main = filepath.Join(root, "main")
	gitT(t, root, "clone", "-q", origin, main)
	gitT(t, main, "config", "user.email", "t@t")
	gitT(t, main, "config", "user.name", "t")
	writeFileT(t, filepath.Join(main, "a.txt"), "one\n")
	gitT(t, main, "add", "a.txt")
	gitT(t, main, "commit", "-qm", "one")
	gitT(t, main, "push", "-q", "-u", "origin", "main")

	slot = filepath.Join(root, "slot1")
	gitT(t, main, "worktree", "add", "--detach", slot, "HEAD")
	return main, slot, origin
}

func writeFileT(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func gitReadT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestGitCheckoutDetached proves the premise of the fix and the helper that
// replaces the refused checkout: git will not check out a branch another
// worktree holds, detaching at the remote-tracking ref does the same job, and
// afterwards the main checkout can still switch to its branch.
func TestGitCheckoutDetached(t *testing.T) {
	main, slot, _ := slotFixture(t)

	// The premise: `git checkout main` in the slot is refused because the main
	// checkout holds that branch. If this ever stops failing, the bug is gone
	// and this whole path can be reconsidered.
	if err := gitEnsureBranch(slot, "main", ""); err == nil {
		t.Fatal("gitEnsureBranch(slot, main) succeeded - git no longer refuses a branch held by another worktree")
	}

	if err := gitCheckoutDetached(slot, "origin/main"); err != nil {
		t.Fatalf("gitCheckoutDetached: %v", err)
	}
	if got, want := gitReadT(t, slot, "rev-parse", "HEAD"), gitReadT(t, slot, "rev-parse", "origin/main"); got != want {
		t.Fatalf("slot HEAD = %s, want origin/main %s", got, want)
	}
	if head := gitReadT(t, slot, "rev-parse", "--abbrev-ref", "HEAD"); head != "HEAD" {
		t.Fatalf("slot is on branch %q, want a detached HEAD", head)
	}

	// The slot must hold no branch at all - that is what frees `main` for the
	// user's checkout and for `git worktree add` during the acceptance.
	list := gitReadT(t, main, "worktree", "list", "--porcelain")
	for _, block := range strings.Split(list, "\n\n") {
		if strings.Contains(block, "worktree "+slot) && strings.Contains(block, "branch refs/heads/main") {
			t.Fatalf("slot still holds refs/heads/main:\n%s", list)
		}
	}
	if out, err := exec.Command("git", "-C", main, "checkout", "main").CombinedOutput(); err != nil {
		t.Fatalf("main checkout can no longer switch to main: %v\n%s", err, out)
	}
}

// TestSlotDetachedBase is the fix as the run uses it: with origin ahead of the
// local base, a slot forks its subs from origin/<base> and leaves the local
// branch (held by the main checkout) untouched.
func TestSlotDetachedBase(t *testing.T) {
	main, slot, origin := slotFixture(t)

	// Push a commit to origin that the local main does not have, from a
	// throwaway clone - so the fixture's local main is genuinely behind.
	other := filepath.Join(t.TempDir(), "other")
	gitT(t, filepath.Dir(other), "clone", "-q", origin, other)
	gitT(t, other, "config", "user.email", "t@t")
	gitT(t, other, "config", "user.name", "t")
	writeFileT(t, filepath.Join(other, "b.txt"), "two\n")
	gitT(t, other, "add", "b.txt")
	gitT(t, other, "commit", "-qm", "two")
	gitT(t, other, "push", "-q", "origin", "main")

	localBefore := gitReadT(t, main, "rev-parse", "main")

	var buf bytes.Buffer
	ref := slotForkRef(&buf, slot, "main")
	if ref != "origin/main" {
		t.Fatalf("slotForkRef = %q, want origin/main (output: %s)", ref, buf.String())
	}
	// The name-only resolver (what --dry-run prints) must agree, without a fetch.
	if got := slotForkRefName(slot, "main"); got != ref {
		t.Fatalf("slotForkRefName = %q, want %q", got, ref)
	}

	originTip := gitReadT(t, slot, "rev-parse", "origin/main")
	if originTip == localBefore {
		t.Fatal("fixture is wrong: origin/main equals the local main")
	}

	// A sub's branch forks from the remote-tracking ref (branchExists would
	// have refused it, so gitFreshBranch must accept any ref).
	if err := gitFreshBranch(slot, "feat/x", ref); err != nil {
		t.Fatalf("gitFreshBranch off %s: %v", ref, err)
	}
	if got := gitReadT(t, slot, "rev-parse", "HEAD"); got != originTip {
		t.Fatalf("feat/x HEAD = %s, want origin/main %s", got, originTip)
	}

	// The local base was neither moved nor checked out by any of it.
	if got := gitReadT(t, main, "rev-parse", "main"); got != localBefore {
		t.Fatalf("local main moved: %s -> %s", localBefore, got)
	}
	if got := gitReadT(t, main, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("main checkout left on %q, want main", got)
	}

	// A local commit origin lacks is a WARNING in a slot (the run proceeds
	// from origin), never freshenBase's divergence error.
	gitT(t, main, "commit", "-qm", "local only", "--allow-empty")
	buf.Reset()
	if ref := slotForkRef(&buf, slot, "main"); ref != "origin/main" {
		t.Fatalf("diverged: slotForkRef = %q, want origin/main", ref)
	}
	if !strings.Contains(buf.String(), "forks from origin/main") {
		t.Fatalf("diverged slot run said nothing about the fork ref: %q", buf.String())
	}
	if err := freshenBase(&bytes.Buffer{}, main, "main"); err == nil {
		t.Fatal("freshenBase (main checkout) accepted a diverged base - the error there must stay")
	}
}

// TestSlotForkRefNoRemote: with no remote (or no counterpart on origin) the
// slot falls back to the local base name, the pre-fix behaviour.
func TestSlotForkRefNoRemote(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir)
	writeFileT(t, filepath.Join(dir, "a.txt"), "one\n")
	gitT(t, dir, "add", "a.txt")
	gitT(t, dir, "commit", "-qm", "one")

	var buf bytes.Buffer
	if got := slotForkRef(&buf, dir, "main"); got != "main" {
		t.Fatalf("slotForkRef without a remote = %q, want main", got)
	}
	if got := slotForkRefName(dir, "main"); got != "main" {
		t.Fatalf("slotForkRefName without a remote = %q, want main", got)
	}
}
