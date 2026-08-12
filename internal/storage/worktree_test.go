package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

	if err := CopyUntrackedFiles(src, dst, nil); err != nil {
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

// TestWorktreeLockRefreshIsAtomic hammers the same-pid refresh path (the epic
// manager re-entering per sub with a new task id) against a concurrent reader.
// The refresh MUST be tmp+rename: an in-place write has a truncated-file window
// in which the reader sees a corrupt lock - and AcquireWorktreeLock treats
// corrupt as stealable, so a rival could take over a LIVE holder's slot.
func TestWorktreeLockRefreshIsAtomic(t *testing.T) {
	dir := t.TempDir()
	pid := os.Getpid()
	if err := AcquireWorktreeLock(dir, "task-0", "run-epic", pid); err != nil {
		t.Fatalf("seed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 200; i++ {
			if err := AcquireWorktreeLock(dir, fmt.Sprintf("task-%d", i), "run-epic", pid); err != nil {
				t.Errorf("refresh %d: %v", i, err)
				return
			}
		}
	}()
	for {
		select {
		case <-done:
			// Refresh scratch files must not linger next to the lock.
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name() != worktreeLockFile {
					t.Fatalf("scratch file left behind: %s", e.Name())
				}
			}
			return
		default:
			lk, err := ReadWorktreeLock(dir)
			if err != nil {
				t.Fatalf("reader saw a torn lock: %v", err)
			}
			if lk == nil || lk.PID != pid {
				t.Fatalf("reader saw a wrong/missing lock: %+v", lk)
			}
		}
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

// TestResolveWorktreeDir covers the path-resolution rules directly. The table
// used to live in internal/cmd (against a resolveWorktreePath wrapper that no
// production code called any more); ResolveWorktrees only exercises the empty
// and relative-sibling cases, so the subdir/absolute/home rules would otherwise
// have gone uncovered when the wrapper was deleted.
func TestResolveWorktreeDir(t *testing.T) {
	proj := "/repos/app"
	home, _ := os.UserHomeDir()

	cases := []struct {
		name string
		wt   string
		want string
	}{
		{"empty defaults to sibling", "", "/repos/app-additional"},
		{"relative sibling", "../app-additional", "/repos/app-additional"},
		{"relative subdir", "worktrees/extra", "/repos/app/worktrees/extra"},
		{"absolute as-is", "/tmp/wt", "/tmp/wt"},
		{"home expansion", "~/wt", filepath.Join(home, "wt")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveWorktreeDir(proj, tc.wt)
			if got != tc.want {
				t.Fatalf("ResolveWorktreeDir(%q) = %q, want %q", tc.wt, got, tc.want)
			}
		})
	}
}

func TestResolveWorktrees(t *testing.T) {
	proj := "/repos/app"

	t.Run("not configured -> nil", func(t *testing.T) {
		if got := (Executor{}).ResolveWorktrees(proj); got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("legacy pair synthesizes one slot", func(t *testing.T) {
		e := Executor{
			AdditionalWorktree: true,
			WorktreePath:       "../app-additional",
			Env:                map[string]string{"PORT": "8090", "SIM": "aaa"},
		}
		got := e.ResolveWorktrees(proj)
		if len(got) != 1 {
			t.Fatalf("expected 1 slot, got %d", len(got))
		}
		if got[0].Path != "/repos/app-additional" {
			t.Fatalf("path = %q", got[0].Path)
		}
		if len(got[0].Env) != 2 || got[0].Env[0] != "PORT=8090" || got[0].Env[1] != "SIM=aaa" {
			t.Fatalf("env = %v", got[0].Env)
		}
	})

	t.Run("worktrees list supersedes legacy pair", func(t *testing.T) {
		e := Executor{
			AdditionalWorktree: true,
			WorktreePath:       "../legacy-ignored",
			Env:                map[string]string{"PORT": "8090", "SHARED": "x"},
			Worktrees: []WorktreeSlot{
				{Path: "../app-additional"},
				{Path: "../app-additional-2", Env: map[string]string{"PORT": "8091", "SIM": "bbb"}},
			},
		}
		got := e.ResolveWorktrees(proj)
		if len(got) != 2 {
			t.Fatalf("expected 2 slots, got %d", len(got))
		}
		if got[0].Path != "/repos/app-additional" || got[1].Path != "/repos/app-additional-2" {
			t.Fatalf("paths = %q, %q", got[0].Path, got[1].Path)
		}
		// Slot 1 inherits the base env untouched.
		want1 := []string{"PORT=8090", "SHARED=x"}
		if len(got[0].Env) != 2 || got[0].Env[0] != want1[0] || got[0].Env[1] != want1[1] {
			t.Fatalf("slot 1 env = %v, want %v", got[0].Env, want1)
		}
		// Slot 2 overlays: PORT overridden, SHARED inherited, SIM added.
		want2 := []string{"PORT=8091", "SHARED=x", "SIM=bbb"}
		if len(got[1].Env) != 3 || got[1].Env[0] != want2[0] || got[1].Env[1] != want2[1] || got[1].Env[2] != want2[2] {
			t.Fatalf("slot 2 env = %v, want %v", got[1].Env, want2)
		}
	})

	t.Run("pathless slots get non-colliding defaults", func(t *testing.T) {
		e := Executor{Worktrees: []WorktreeSlot{{}, {}, {}}}
		got := e.ResolveWorktrees(proj)
		want := []string{"/repos/app-additional", "/repos/app-additional-2", "/repos/app-additional-3"}
		for i, w := range want {
			if got[i].Path != w {
				t.Fatalf("slot %d path = %q, want %q", i+1, got[i].Path, w)
			}
		}
	})
}

// TestCopyUntrackedFilesExcludesAndSymlinks verifies the seeding fix: dependency
// dirs are skipped at any depth (node_modules held ~87k files and its symlinks
// arrived as broken plain copies) and seeded symlinks stay symlinks.
func TestCopyUntrackedFilesExcludesAndSymlinks(t *testing.T) {
	src := t.TempDir()
	gitInit(t, src)

	os.WriteFile(filepath.Join(src, ".gitignore"), []byte("node_modules/\n.env\n"), 0644)
	os.WriteFile(filepath.Join(src, ".env"), []byte("SECRET=1\n"), 0600)
	os.MkdirAll(filepath.Join(src, "node_modules", "pkg"), 0755)
	os.WriteFile(filepath.Join(src, "node_modules", "pkg", "index.js"), []byte("x"), 0644)
	os.MkdirAll(filepath.Join(src, "packages", "a", "node_modules"), 0755)
	os.WriteFile(filepath.Join(src, "packages", "a", "node_modules", "dep.js"), []byte("x"), 0644)
	os.MkdirAll(filepath.Join(src, "ios", "Pods"), 0755)
	os.WriteFile(filepath.Join(src, "ios", "Pods", "Pod.h"), []byte("x"), 0644)
	// An untracked symlink (like node_modules/.bin entries, but outside excludes).
	os.WriteFile(filepath.Join(src, "real.sh"), []byte("#!/bin/sh\n"), 0755)
	os.Symlink("real.sh", filepath.Join(src, "link.sh"))

	dst := t.TempDir()
	if err := CopyUntrackedFiles(src, dst, nil); err != nil {
		t.Fatalf("CopyUntrackedFiles: %v", err)
	}

	if got, _ := os.ReadFile(filepath.Join(dst, ".env")); string(got) != "SECRET=1\n" {
		t.Fatalf("config not seeded: %q", got)
	}
	for _, p := range []string{
		filepath.Join(dst, "node_modules"),
		filepath.Join(dst, "packages", "a", "node_modules"),
		filepath.Join(dst, "ios", "Pods"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("excluded dir was seeded: %s", p)
		}
	}
	info, err := os.Lstat(filepath.Join(dst, "link.sh"))
	if err != nil {
		t.Fatalf("symlink not seeded: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was dereferenced into a plain copy")
	}
	if target, _ := os.Readlink(filepath.Join(dst, "link.sh")); target != "real.sh" {
		t.Fatalf("symlink target = %q", target)
	}
}

func TestSeedExcludesOverride(t *testing.T) {
	if got := (Executor{}).SeedExcludes(); len(got) != len(DefaultSeedExcludes) {
		t.Fatalf("default excludes = %v", got)
	}
	custom := Executor{SeedExclude: []string{"vendor/"}}
	if got := custom.SeedExcludes(); len(got) != 1 || got[0] != "vendor/" {
		t.Fatalf("override must REPLACE defaults, got %v", got)
	}
	if !seedExcluded("vendor/x.go", []string{"vendor/"}) || seedExcluded("node_modules/x", []string{"vendor/"}) {
		t.Fatal("override matching wrong")
	}
}

// TestAcquireWorktreeLockAtomicRace hammers the O_EXCL claim: two live
// acquirers (distinct pids) race for the same slot; exactly one must win and
// the loser must get a busy error naming the winner - never two winners, never
// two losers.
// A pid outliving its owner is the ordinary case after a reboot or a kill -9:
// the lock file survives, the number gets handed to somebody else, and a
// liveness check that only asks "does this pid exist" keeps the slot busy until
// a human deletes the file. pid 1 is the cheapest stand-in - alive on every
// machine, and demonstrably NOT the process that stamped a lock dated 2020.
func TestWorktreeLockIgnoresRecycledPID(t *testing.T) {
	seed := func(t *testing.T, dir string, started string) {
		t.Helper()
		lk := WorktreeLock{PID: 1, TaskID: "task-gone", Kind: "run-epic", Started: started}
		data, err := json.Marshal(lk)
		if err != nil {
			t.Fatalf("marshal lock: %v", err)
		}
		if err := os.WriteFile(worktreeLockPath(dir), data, 0644); err != nil {
			t.Fatalf("seed lock: %v", err)
		}
	}

	t.Run("a lock older than its holder is stale", func(t *testing.T) {
		dir := t.TempDir()
		seed(t, dir, "2020-01-01T00:00:00Z")

		if h := LiveWorktreeHolder(dir); h != nil {
			t.Errorf("holder = %+v, want nil - pid 1 did not start before 2020", h)
		}
		if err := AcquireWorktreeLock(dir, "task-new", "work", os.Getpid()); err != nil {
			t.Fatalf("expected takeover of a recycled-pid lock, got: %v", err)
		}
		lk, _ := ReadWorktreeLock(dir)
		if lk == nil || lk.TaskID != "task-new" || lk.PID != os.Getpid() {
			t.Fatalf("takeover did not rewrite lock: %+v", lk)
		}
	})

	// The other direction matters just as much: a live holder must keep its
	// slot. Reading it as dead would hand a running worker's worktree to a
	// second run.
	t.Run("a live holder still holds", func(t *testing.T) {
		dir := t.TempDir()
		seed(t, dir, time.Now().Format(time.RFC3339))

		if h := LiveWorktreeHolder(dir); h == nil {
			t.Error("holder = nil, want the live pid-1 lock")
		}
		var busy *WorktreeBusyError
		err := AcquireWorktreeLock(dir, "task-new", "work", os.Getpid())
		if !errors.As(err, &busy) {
			t.Fatalf("err = %v, want a busy error", err)
		}
	})
}

func TestAcquireWorktreeLockAtomicRace(t *testing.T) {
	pids := []int{os.Getpid(), os.Getppid()} // both alive + signalable
	for i := 0; i < 50; i++ {
		dir := t.TempDir()
		var wg sync.WaitGroup
		errs := make([]error, len(pids))
		for j, pid := range pids {
			wg.Add(1)
			go func(j, pid int) {
				defer wg.Done()
				errs[j] = AcquireWorktreeLock(dir, "task-race", "work", pid)
			}(j, pid)
		}
		wg.Wait()

		wins := 0
		for j, err := range errs {
			if err == nil {
				wins++
				continue
			}
			var busy *WorktreeBusyError
			if !errors.As(err, &busy) {
				t.Fatalf("iteration %d: acquirer %d got non-busy error: %v", i, j, err)
			}
		}
		if wins != 1 {
			t.Fatalf("iteration %d: expected exactly 1 winner, got %d (errs: %v)", i, wins, errs)
		}
	}
}

// TestWorktreeLockReleaseVsTakeoverRace reproduces the release TOCTOU: a
// process cleans up a lock on behalf of a pid that has already died (killRun
// releasing on a worker's behalf from a DIFFERENT process), and - in the gap
// between ReleaseWorktreeLock's ownership read and its rename - a rival
// judges that same dead pid stale and lands its own fresh lock. The real
// window is a single read-then-rename with nothing externally observable in
// between, so a live goroutine race is not a reliable way to land inside it -
// releaseRaceHook forces the exact adversarial ordering deterministically
// instead (the hook only ever runs from ReleaseWorktreeLock itself, so it
// cannot re-enter; it self-disarms only as a defensive habit against a future
// second call site). The assertions that catch a lost lock: the rival's fresh
// lock must survive ON DISK, release must report that it lost the race rather
// than claim success, and no scratch litter is left behind - a release that
// unlinked-by-path instead of verifying what it renamed aside would instead
// silently delete the rival's winning lock, even though AcquireWorktreeLock
// had already returned nil to its caller.
func TestWorktreeLockReleaseVsTakeoverRace(t *testing.T) {
	const deadPID = 2147483646
	livePID := os.Getpid()

	dir := t.TempDir()
	if err := AcquireWorktreeLock(dir, "task-crashed", "run-epic", deadPID); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}

	prev := releaseRaceHook
	t.Cleanup(func() { releaseRaceHook = prev })
	releaseRaceHook = func() {
		releaseRaceHook = func() {} // defensive: must not fire again even if a future call site adds a second invocation
		if err := AcquireWorktreeLock(dir, "task-new", "work", livePID); err != nil {
			t.Errorf("rival takeover inside the race window: %v", err)
		}
	}

	relErr := ReleaseWorktreeLock(dir, deadPID)
	if relErr == nil {
		t.Fatalf("release must report that it lost the race, not silently succeed")
	}

	lk, err := ReadWorktreeLock(dir)
	if err != nil {
		t.Fatalf("read after race: %v", err)
	}
	if lk == nil || lk.TaskID != "task-new" || lk.PID != livePID {
		t.Fatalf("a release racing a concurrent takeover must never erase the rival's fresh lock, got %+v", lk)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != worktreeLockFile {
			t.Fatalf("scratch file left behind after the race: %s", e.Name())
		}
	}
}

// TestWorktreeLockReleaseVsSamePidRefreshRace covers the half of
// ReleaseWorktreeLock's post-rename identity check that
// TestWorktreeLockReleaseVsTakeoverRace cannot reach: a SAME pid re-entering
// its own lock for a new task (AcquireWorktreeLock's refresh branch, which
// rewrites Started under an unchanged PID) lands in the release's race window.
// A PID-only re-check cannot tell this from an untouched lock - it takes the
// Started comparison too, which in turn depends on AcquireWorktreeLock
// stamping with enough precision that two refreshes moments apart never read
// back identical (see the nanosecond-precision comment on AcquireWorktreeLock).
func TestWorktreeLockReleaseVsSamePidRefreshRace(t *testing.T) {
	const pid = 999999 // never checked for liveness on the self-refresh path
	dir := t.TempDir()
	if err := AcquireWorktreeLock(dir, "task-old", "work", pid); err != nil {
		t.Fatalf("seed: %v", err)
	}

	prev := releaseRaceHook
	t.Cleanup(func() { releaseRaceHook = prev })
	releaseRaceHook = func() {
		releaseRaceHook = func() {}
		if err := AcquireWorktreeLock(dir, "task-new", "work", pid); err != nil {
			t.Fatalf("same-pid refresh inside the race window: %v", err)
		}
	}

	relErr := ReleaseWorktreeLock(dir, pid)
	if relErr == nil {
		t.Fatalf("release must not silently succeed over a same-pid refresh that landed in the race window")
	}

	lk, err := ReadWorktreeLock(dir)
	if err != nil {
		t.Fatalf("read after race: %v", err)
	}
	if lk == nil || lk.TaskID != "task-new" || lk.PID != pid {
		t.Fatalf("a release racing a concurrent same-pid refresh must never erase the refreshed lock, got %+v", lk)
	}
}
