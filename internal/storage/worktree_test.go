package storage

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitInit creates a git repo with an initial commit in dir.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-q", "-m", "init")
}

func TestCopyUntrackedFiles(t *testing.T) {
	src := t.TempDir()
	gitInit(t, src)

	// A gitignored config (the case we care about) and a plain untracked file.
	if err := os.WriteFile(filepath.Join(src, ".gitignore"), []byte(".env\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".env"), []byte("SECRET=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "ios"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "ios", "Config.plist"), []byte("plist\n"), 0644); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	// Pre-existing destination file must NOT be clobbered.
	if err := os.WriteFile(filepath.Join(dst, ".env"), []byte("KEEP\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CopyUntrackedFiles(src, dst); err != nil {
		t.Fatalf("CopyUntrackedFiles: %v", err)
	}

	// Nested untracked file copied (dirs created).
	if got, err := os.ReadFile(filepath.Join(dst, "ios", "Config.plist")); err != nil || string(got) != "plist\n" {
		t.Fatalf("nested untracked not copied: got %q err %v", got, err)
	}
	// Existing destination preserved.
	if got, _ := os.ReadFile(filepath.Join(dst, ".env")); string(got) != "KEEP\n" {
		t.Fatalf("existing dst clobbered: got %q", got)
	}
}

func TestEnsureWorktreeCreateAndReuse(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)
	wt := filepath.Join(t.TempDir(), "additional")

	if err := EnsureWorktree(repo, wt); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if !isInsideWorkTree(wt) {
		t.Fatalf("expected %s to be a git worktree", wt)
	}
	// Marker file survives a reuse call (no re-add / clobber).
	marker := filepath.Join(wt, "node_modules_marker")
	if err := os.WriteFile(marker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorktree(repo, wt); err != nil {
		t.Fatalf("reuse worktree: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("reuse must not clobber worktree contents: %v", err)
	}

	// A path that exists but is not a worktree is refused.
	notWt := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(notWt, 0755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorktree(repo, notWt); err == nil {
		t.Fatalf("expected refusal for a non-worktree existing path")
	}
}

func TestWorktreeLockLifecycle(t *testing.T) {
	dir := t.TempDir()
	livePID := os.Getpid()

	// Acquire on a free worktree.
	if err := AcquireWorktreeLock(dir, "task-a", "work", livePID); err != nil {
		t.Fatalf("acquire on free: %v", err)
	}
	lk, err := ReadWorktreeLock(dir)
	if err != nil || lk == nil || lk.TaskID != "task-a" || lk.PID != livePID {
		t.Fatalf("readback: lk=%+v err=%v", lk, err)
	}

	// Same pid re-acquires idempotently (epic manager re-entering per sub).
	if err := AcquireWorktreeLock(dir, "task-a", "work", livePID); err != nil {
		t.Fatalf("same-pid re-acquire: %v", err)
	}

	// A different LIVE holder is busy.
	if err := AcquireWorktreeLock(dir, "task-b", "run-epic", 424242); err == nil {
		t.Fatalf("expected busy error")
	} else {
		var busy *WorktreeBusyError
		if !errors.As(err, &busy) {
			t.Fatalf("expected *WorktreeBusyError, got %T: %v", err, err)
		}
		if busy.Holder.TaskID != "task-a" {
			t.Fatalf("busy holder = %+v", busy.Holder)
		}
	}

	// Release only frees a lock owned by pid: a wrong pid is a no-op.
	if err := ReleaseWorktreeLock(dir, 424242); err != nil {
		t.Fatalf("release wrong pid: %v", err)
	}
	if lk, _ := ReadWorktreeLock(dir); lk == nil {
		t.Fatalf("wrong-pid release must not drop the lock")
	}
	// Owner release drops it.
	if err := ReleaseWorktreeLock(dir, livePID); err != nil {
		t.Fatalf("release owner: %v", err)
	}
	if lk, _ := ReadWorktreeLock(dir); lk != nil {
		t.Fatalf("lock should be gone after owner release")
	}
	// Idempotent release on a free worktree.
	if err := ReleaseWorktreeLock(dir, livePID); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
}

func TestWorktreeLockStaleTakeover(t *testing.T) {
	dir := t.TempDir()
	const deadPID = 2147483646

	// A lock held by a dead process is stale.
	if err := AcquireWorktreeLock(dir, "task-crashed", "run-epic", deadPID); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}
	// A fresh run takes it over automatically - no busy error.
	if err := AcquireWorktreeLock(dir, "task-new", "work", os.Getpid()); err != nil {
		t.Fatalf("expected stale takeover, got: %v", err)
	}
	lk, _ := ReadWorktreeLock(dir)
	if lk == nil || lk.TaskID != "task-new" || lk.PID != os.Getpid() {
		t.Fatalf("takeover did not rewrite lock: %+v", lk)
	}
}
