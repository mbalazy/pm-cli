package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// TestPrepareWorktree exercises the cmd-level integration: ensure + seed configs
// + lock, then release. Uses a real git repo, no claude.
func TestPrepareWorktree(t *testing.T) {
	repo := t.TempDir()
	gitRun := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitRun("init", "-q")
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0644)
	gitRun("add", "README.md")
	gitRun("commit", "-q", "-m", "init")
	// A gitignored config that must be seeded into the worktree.
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0644)
	os.WriteFile(filepath.Join(repo, ".env"), []byte("PORT=8090\n"), 0600)

	proj := &storage.Project{Path: repo}
	wt := filepath.Join(t.TempDir(), "additional")

	release, err := prepareWorktree(proj, wt, "app-1", "work")
	if err != nil {
		t.Fatalf("prepareWorktree: %v", err)
	}
	if !isInsideWorkTreeCmd(t, wt) {
		t.Fatalf("worktree not created at %s", wt)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, ".env")); string(got) != "PORT=8090\n" {
		t.Fatalf("config not seeded into worktree: %q", got)
	}
	if lk, _ := storage.ReadWorktreeLock(wt); lk == nil || lk.TaskID != "app-1" {
		t.Fatalf("lock not held: %+v", lk)
	}
	release()
	if lk, _ := storage.ReadWorktreeLock(wt); lk != nil {
		t.Fatalf("release did not free the lock: %+v", lk)
	}
}

func isInsideWorkTreeCmd(t *testing.T, dir string) bool {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// TestPlanWorkAdditionalGating verifies the inverted default: without
// --additional a run targets the main checkout unchanged; with --additional it
// switches to the configured worktree; and --additional on an unconfigured
// project is a clear error.
// firstLines returns the first n lines of s, for compact failure messages.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func TestPlanWorkAdditionalGating(t *testing.T) {
	root := t.TempDir()
	store := &storage.Store{Root: root}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "checkout", "-q", "-b", "development")
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	proj := &storage.Project{Name: "App", Path: repo, Executor: &storage.Executor{
		Enabled:            true,
		AdditionalWorktree: true,
		WorktreePath:       "../app-additional",
		BaseBranch:         "development",
		Env:                map[string]string{"ADDITIONAL_METRO_PORT": "8090"},
	}}
	if err := store.CreateProject("app", proj); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "Task", Status: storage.StatusTodo}}
	if err := store.AddTask("app", task); err != nil {
		t.Fatalf("add task: %v", err)
	}

	t.Run("default = main checkout, no worktree", func(t *testing.T) {
		plan, err := planWork(store, task, "app", workOptions{standalone: true})
		if err != nil {
			t.Fatalf("planWork: %v", err)
		}
		if plan.worktree {
			t.Fatal("default run must not use the additional worktree")
		}
		if plan.workDir != repo {
			t.Fatalf("workDir = %q, want main checkout %q", plan.workDir, repo)
		}
		if plan.base != "" || plan.env != nil {
			t.Fatalf("default run must not resolve base/env: base=%q env=%v", plan.base, plan.env)
		}
	})

	t.Run("--additional switches to the configured worktree", func(t *testing.T) {
		plan, err := planWork(store, task, "app", workOptions{standalone: true, additional: true})
		if err != nil {
			t.Fatalf("planWork: %v", err)
		}
		if !plan.worktree {
			t.Fatal("--additional must use the worktree")
		}
		if want := storage.ResolveWorktreeDir(proj.Path, proj.Executor.WorktreePath); plan.workDir != want {
			t.Fatalf("workDir = %q, want %q", plan.workDir, want)
		}
		if plan.base != "development" {
			t.Fatalf("base = %q, want development (from base_branch)", plan.base)
		}
		if len(plan.env) != 1 || plan.env[0] != "ADDITIONAL_METRO_PORT=8090" {
			t.Fatalf("env = %v", plan.env)
		}
	})

	t.Run("prompt states the dir the worker runs in, not the main checkout", func(t *testing.T) {
		// --additional: the plan (provisionally slot 1) must already state the
		// slot path - printing proj.Path would hand the isolated worker an
		// absolute path back into the main checkout.
		plan, err := planWork(store, task, "app", workOptions{standalone: true, additional: true})
		if err != nil {
			t.Fatalf("planWork: %v", err)
		}
		wt := storage.ResolveWorktreeDir(proj.Path, proj.Executor.WorktreePath)
		if !strings.Contains(plan.prompt, "Repo path: "+wt) {
			t.Fatalf("prompt must state the slot path %q, got:\n%s", wt, firstLines(plan.prompt, 6))
		}
		if strings.Contains(plan.prompt, "Repo path: "+repo) {
			t.Fatal("prompt must not leak the main checkout path")
		}

		// retarget (the standalone claim landing on a different slot) must
		// re-render both the prompt and the argv it is embedded in.
		plan.retarget("/claimed/slot-2", []string{"K=V"})
		if !strings.Contains(plan.prompt, "Repo path: /claimed/slot-2") {
			t.Fatalf("retargeted prompt must state the claimed slot, got:\n%s", firstLines(plan.prompt, 6))
		}
		if plan.cmdArgs[1] != plan.prompt {
			t.Fatal("retarget must rebuild cmdArgs from the new prompt")
		}
		if plan.workDir != "/claimed/slot-2" || len(plan.env) != 1 {
			t.Fatalf("retarget must move workDir/env: %q %v", plan.workDir, plan.env)
		}
	})

	t.Run("epic sub with a pre-claimed slot states that slot", func(t *testing.T) {
		plan, err := planWork(store, task, "app", workOptions{
			standalone: false, additional: true, slotDir: "/mgr/claimed", slotEnv: []string{"A=1"},
		})
		if err != nil {
			t.Fatalf("planWork: %v", err)
		}
		if !strings.Contains(plan.prompt, "Repo path: /mgr/claimed") {
			t.Fatalf("epic sub prompt must state the manager's claimed slot, got:\n%s", firstLines(plan.prompt, 6))
		}
	})

	t.Run("--additional on an unconfigured project errors", func(t *testing.T) {
		plain := &storage.Project{Name: "Plain", Path: repo, Executor: &storage.Executor{Enabled: true}}
		if err := store.CreateProject("plain", plain); err != nil {
			t.Fatalf("create project: %v", err)
		}
		pt := &storage.Task{Meta: storage.TaskMeta{ID: "plain-1", Title: "T", Status: storage.StatusTodo}}
		store.AddTask("plain", pt)
		_, err := planWork(store, pt, "plain", workOptions{standalone: true, additional: true})
		if err == nil || !strings.Contains(err.Error(), "no worktree slots configured") {
			t.Fatalf("expected 'not configured' error, got %v", err)
		}
	})
}

// TestPlanWorkRejectsEmptyResolvedBase covers the silent-empty-base bug: with
// no --base flag and no executor.base_branch, planWork falls back to the main
// checkout's current branch (gitCurrentBranch). If that read fails, the old
// code folded the error away (`cur, _ := ...`) and resolveWorktreeBase
// happily returned "" - which gitFreshBranch treats as "branch from whatever
// the reused worktree's HEAD currently is" (silently forking off a previous
// task's leftover branch). planWork must now refuse to hand out an empty
// base, in both the real run and the --dry-run path (which calls planWork
// too, before ever reaching gitFreshBranch).
func TestPlanWorkRejectsEmptyResolvedBase(t *testing.T) {
	root := t.TempDir()
	store := &storage.Store{Root: root}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "checkout", "-q", "-b", "development")
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	// No base_branch configured - the fallback (main checkout's current
	// branch) is the only source of a base, and that read is about to fail.
	proj := &storage.Project{Name: "NoBase", Path: repo, Executor: &storage.Executor{
		Enabled:            true,
		AdditionalWorktree: true,
		WorktreePath:       "../nobase-additional",
	}}
	if err := store.CreateProject("nobase", proj); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "nobase-1", Title: "Task", Status: storage.StatusTodo}}
	if err := store.AddTask("nobase", task); err != nil {
		t.Fatalf("add task: %v", err)
	}

	fakeGitFailing(t, " rev-parse --abbrev-ref HEAD", "fatal: fake HEAD read failure")

	_, err := planWork(store, task, "nobase", workOptions{standalone: true, additional: true})
	if err == nil {
		t.Fatal("expected planWork to reject an empty resolved base, got nil error")
	}
	if !strings.Contains(err.Error(), "base") {
		t.Fatalf("error should mention the base branch resolution failure, got: %v", err)
	}
}

// TestPlanWorkEpicSubIgnoresUnresolvableMainCheckoutBase is the flip side of
// TestPlanWorkRejectsEmptyResolvedBase: an epic sub's plan.base is NEVER
// consumed (executeWork only reads it under opts.standalone; the epic
// manager resolves its own base - epicBranch/baseBranch in run_epic.go -
// directly). A failed gitCurrentBranch read on the main checkout (unrelated
// to the epic's actual worktree/base) must not fail an epic sub over a value
// it was never going to use - scoping the empty-base guard to opts.standalone
// only is what keeps this call path unaffected.
func TestPlanWorkEpicSubIgnoresUnresolvableMainCheckoutBase(t *testing.T) {
	root := t.TempDir()
	store := &storage.Store{Root: root}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "checkout", "-q", "-b", "development")
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	proj := &storage.Project{Name: "NoBase", Path: repo, Executor: &storage.Executor{
		Enabled:            true,
		AdditionalWorktree: true,
		WorktreePath:       "../nobase-additional",
	}}
	if err := store.CreateProject("nobase", proj); err != nil {
		t.Fatalf("create project: %v", err)
	}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "nobase-1", Title: "Task", Status: storage.StatusTodo}}
	if err := store.AddTask("nobase", task); err != nil {
		t.Fatalf("add task: %v", err)
	}

	fakeGitFailing(t, " rev-parse --abbrev-ref HEAD", "fatal: fake HEAD read failure")

	// Epic sub call: standalone false, a slot already claimed by the manager
	// (slotDir set), same as driveSub/driveSubIndependent in run_epic.go.
	_, err := planWork(store, task, "nobase", workOptions{
		standalone: false, additional: true, slotDir: "/mgr/claimed", slotEnv: nil,
	})
	if err != nil {
		t.Fatalf("epic sub planWork must not fail on an unresolvable main-checkout base it never uses, got: %v", err)
	}
}

func TestResolveWorktreeBase(t *testing.T) {
	cases := []struct {
		name     string
		flag     string
		execBase string
		fallback string
		want     string
	}{
		{"explicit flag wins over everything", "release/1", "development", "cur", "release/1"},
		{"flag beats project base_branch", "hotfix", "development", "cur", "hotfix"},
		{"project base_branch when no flag", "", "development", "cur", "development"},
		{"blank flag is treated as unset", "  ", "development", "cur", "development"},
		{"fallback when neither set", "", "", "cur", "cur"},
		{"fallback main for epic", "", "", "main", "main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveWorktreeBase(tc.flag, tc.execBase, tc.fallback); got != tc.want {
				t.Fatalf("resolveWorktreeBase(%q,%q,%q) = %q, want %q", tc.flag, tc.execBase, tc.fallback, got, tc.want)
			}
		})
	}
}

// TestExecutorWorktreeYAML verifies the new schema fields round-trip and that a
// project without an executor block keeps worktree off.
func TestExecutorWorktreeYAML(t *testing.T) {
	yaml := `name: orbit
path: /repos/app
executor:
  additional_worktree: true
  worktree_path: ../app-additional
  base_branch: development
  env:
    ADDITIONAL_METRO_PORT: "8090"
`
	dir := t.TempDir()
	p := filepath.Join(dir, "project.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	proj, err := storage.ReadProject(p)
	if err != nil {
		t.Fatal(err)
	}
	exec := proj.GetExecutor()
	if !exec.AdditionalWorktree {
		t.Fatal("additional_worktree should be true")
	}
	if exec.WorktreePath != "../app-additional" {
		t.Fatalf("worktree_path = %q", exec.WorktreePath)
	}
	if exec.BaseBranch != "development" {
		t.Fatalf("base_branch = %q", exec.BaseBranch)
	}
	// Precedence: no --base flag -> project base_branch wins over the fallback.
	if got := resolveWorktreeBase("", exec.BaseBranch, "cur-branch"); got != "development" {
		t.Fatalf("resolved base = %q, want development", got)
	}
	if exec.Env["ADDITIONAL_METRO_PORT"] != "8090" {
		t.Fatalf("env not parsed: %v", exec.Env)
	}
	if got := storage.ResolveWorktreeDir(proj.Path, exec.WorktreePath); got != "/repos/app-additional" {
		t.Fatalf("resolved = %q", got)
	}
	if slice := exec.EnvSlice(); len(slice) != 1 || !strings.HasPrefix(slice[0], "ADDITIONAL_METRO_PORT=") {
		t.Fatalf("env slice = %v", slice)
	}

	// No executor block -> additional_worktree stays off (opt-in).
	if (&storage.Project{}).GetExecutor().AdditionalWorktree {
		t.Fatal("default executor must have additional_worktree off")
	}
}

// TestAcquireWorktreeSlot exercises the pool allocation: first free slot wins,
// a busy slot is skipped, --slot pins, and an exhausted pool errors with every
// holder listed.
func TestAcquireWorktreeSlot(t *testing.T) {
	newRepo := func(t *testing.T) *storage.Project {
		repo := t.TempDir()
		gitT(t, repo, "init", "-q")
		os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
		gitT(t, repo, "add", ".")
		gitT(t, repo, "commit", "-q", "-m", "init")
		return &storage.Project{Path: repo}
	}
	newSlots := func(t *testing.T) []storage.ResolvedWorktree {
		base := t.TempDir()
		return []storage.ResolvedWorktree{
			{Path: filepath.Join(base, "slot1"), Env: []string{"PORT=8090"}},
			{Path: filepath.Join(base, "slot2"), Env: []string{"PORT=8091"}},
		}
	}
	// A live pid that is NOT ours: our own pid re-acquires idempotently, which
	// would defeat the busy check. The test runner's parent is alive + signalable.
	otherLivePID := os.Getppid()

	t.Run("first free slot wins", func(t *testing.T) {
		proj, slots := newRepo(t), newSlots(t)
		slot, release, err := acquireWorktreeSlot(proj, "app", slots, 0, "app-1", "work")
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer release()
		if slot.Path != slots[0].Path {
			t.Fatalf("claimed %q, want slot 1 %q", slot.Path, slots[0].Path)
		}
		if lk, _ := storage.ReadWorktreeLock(slot.Path); lk == nil || lk.TaskID != "app-1" {
			t.Fatalf("lock not held on claimed slot: %+v", lk)
		}
	})

	t.Run("busy slot 1 falls through to slot 2", func(t *testing.T) {
		proj, slots := newRepo(t), newSlots(t)
		os.MkdirAll(slots[0].Path, 0755)
		if err := storage.AcquireWorktreeLock(slots[0].Path, "app-other", "work", otherLivePID); err != nil {
			t.Fatalf("seed busy lock: %v", err)
		}
		slot, release, err := acquireWorktreeSlot(proj, "app", slots, 0, "app-1", "work")
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer release()
		if slot.Path != slots[1].Path {
			t.Fatalf("claimed %q, want slot 2 %q", slot.Path, slots[1].Path)
		}
		if len(slot.Env) != 1 || slot.Env[0] != "PORT=8091" {
			t.Fatalf("claimed slot env = %v, want slot 2's", slot.Env)
		}
	})

	t.Run("all slots busy lists every holder", func(t *testing.T) {
		proj, slots := newRepo(t), newSlots(t)
		for _, s := range slots {
			os.MkdirAll(s.Path, 0755)
			if err := storage.AcquireWorktreeLock(s.Path, "app-other", "work", otherLivePID); err != nil {
				t.Fatalf("seed busy lock: %v", err)
			}
		}
		_, _, err := acquireWorktreeSlot(proj, "app", slots, 0, "app-1", "work")
		if err == nil {
			t.Fatal("expected all-busy error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "all 2 worktree slot(s) busy") ||
			!strings.Contains(msg, slots[0].Path) || !strings.Contains(msg, slots[1].Path) {
			t.Fatalf("error must list both holders, got: %v", err)
		}
	})

	t.Run("pin claims that slot even when slot 1 is free", func(t *testing.T) {
		proj, slots := newRepo(t), newSlots(t)
		slot, release, err := acquireWorktreeSlot(proj, "app", slots, 2, "app-1", "work")
		if err != nil {
			t.Fatalf("acquire pinned: %v", err)
		}
		defer release()
		if slot.Path != slots[1].Path {
			t.Fatalf("claimed %q, want pinned slot 2 %q", slot.Path, slots[1].Path)
		}
	})

	t.Run("pinned busy slot fails fast", func(t *testing.T) {
		proj, slots := newRepo(t), newSlots(t)
		os.MkdirAll(slots[1].Path, 0755)
		if err := storage.AcquireWorktreeLock(slots[1].Path, "app-other", "work", otherLivePID); err != nil {
			t.Fatalf("seed busy lock: %v", err)
		}
		_, _, err := acquireWorktreeSlot(proj, "app", slots, 2, "app-1", "work")
		if err == nil || !strings.Contains(err.Error(), "slot 2") {
			t.Fatalf("expected pinned-busy error mentioning slot 2, got %v", err)
		}
	})

	t.Run("pin out of range errors", func(t *testing.T) {
		proj, slots := newRepo(t), newSlots(t)
		_, _, err := acquireWorktreeSlot(proj, "app", slots, 3, "app-1", "work")
		if err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Fatalf("expected out-of-range error, got %v", err)
		}
	})
}

func TestRunPrepare(t *testing.T) {
	dir := t.TempDir()
	if err := runPrepare(dir, "echo ok > prepared.txt"); err != nil {
		t.Fatalf("runPrepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "prepared.txt")); err != nil {
		t.Fatalf("prepare did not run in the worktree dir: %v", err)
	}
	if err := runPrepare(dir, "exit 3"); err == nil {
		t.Fatal("failing prepare must error")
	}
}
