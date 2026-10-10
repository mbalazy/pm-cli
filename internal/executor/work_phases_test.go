package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// workGreenWorker is the fake worker of the tests below: one commit, a
// verified envelope.
var workGreenWorker = "git commit -q --allow-empty -m 'worker commit'\necho '" + envelope(workerVerified, "all green") + "'"

func workMutateExecutor(t *testing.T, store *storage.Store, fn func(e *storage.Executor)) {
	t.Helper()
	if _, err := store.MutateProject("app", func(p *storage.Project) error {
		fn(p.Executor)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func workJournal(t *testing.T, store *storage.Store) []storage.JournalEntry {
	t.Helper()
	entries, err := storage.ReadJournal(store.ProjectDir("app"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// workHeadTo is a shell step that appends the branch checked out where it runs
// (or HEAD when detached) to file.
func workHeadTo(file string) string {
	return "git rev-parse --abbrev-ref HEAD >> " + file
}

// workInSlot plans a standalone --additional run of app-1-1 on the slot
// fixture and claims the slot the way `pm work` does, leaving executeWork to
// the test. mutate edits the executor and the task before planning.
func workInSlot(t *testing.T, mutate func(e *storage.Executor, sub *storage.Task)) (*storage.Store, *storage.Task, *workPlan, workOptions, string, string) {
	t.Helper()
	store, repo, slot := epicSlotFixture(t)
	// The fixture's local main is a commit behind origin; catch it up so the
	// fresh branch has nothing to fast-forward (a slot cannot move a branch
	// the main checkout holds).
	gitT(t, repo, "pull", "-q", "--ff-only")
	sub, _ := store.FindTask("app", "app-1-1")
	if mutate != nil {
		workMutateExecutor(t, store, func(e *storage.Executor) { mutate(e, sub) })
		if err := store.WriteTask(sub); err != nil {
			t.Fatal(err)
		}
	}
	sub, _ = store.FindTask("app", "app-1-1")
	var errOut strings.Builder
	opts := workOptions{standalone: true, additional: true, model: "opus", maxTurns: 10, timeout: 30 * 1e9, errOut: &errOut}
	plan, err := planWork(store, sub, "app", opts)
	if err != nil {
		t.Fatalf("planWork: %v", err)
	}
	claimed, release, err := acquireWorktreeSlot(plan.proj, "app", plan.slots, 0, sub.Meta.ID, "work")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	plan.retarget(claimed.Path, claimed.Env)
	return store, sub, plan, opts, repo, slot
}

func workStderr(opts workOptions) string { return opts.errOut.(*strings.Builder).String() }

// TestExecuteWorkInASlot pins a standalone run in a worktree slot: leftovers
// wiped, a fresh branch off the base, prepare and the rig check before the
// worker, the worker in the slot, both recorded in the journal.
func TestExecuteWorkInASlot(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	t.Setenv("PM_TEST_SEEN", seen)
	fakeClaude(t, `echo "worker $(pwd -P)" >> "$PM_TEST_SEEN"`+"\n"+workGreenWorker)
	store, task, plan, opts, repo, slot := workInSlot(t, func(e *storage.Executor, sub *storage.Task) {
		e.Prepare = workHeadTo(seen)
		e.Rig = "echo rig-ran >> " + seen
		sub.Meta.Runtime = storage.RuntimeOn
	})
	// A leftover from an earlier run in the slot.
	if err := os.WriteFile(filepath.Join(slot, "f.txt"), []byte("leftover\n"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := executeWork(store, task, plan, opts)
	if err != nil {
		t.Fatalf("executeWork: %v\n%s", err, workStderr(opts))
	}
	if res.Status != workerVerified {
		t.Errorf("status = %q", res.Status)
	}
	data, _ := os.ReadFile(seen)
	slotReal, _ := filepath.EvalSymlinks(slot)
	want := plan.branch + "|rig-ran|worker " + slotReal
	if got := strings.Join(strings.Split(strings.TrimSpace(string(data)), "\n"), "|"); got != want {
		t.Errorf("prepare, rig and worker saw %q, want %q", got, want)
	}
	if !strings.Contains(workStderr(opts), "pm work: prepare in "+slot+": "+workHeadTo(seen)) {
		t.Errorf("prepare must be announced:\n%s", workStderr(opts))
	}
	if b, _ := os.ReadFile(filepath.Join(slot, "f.txt")); string(b) != "x\n" {
		t.Errorf("the leftover survived: %q", b)
	}
	if got, want := gitReadT(t, slot, "rev-parse", plan.branch+"^"), gitReadT(t, repo, "rev-parse", "main"); got != want {
		t.Errorf("%s forked from %s, want main %s", plan.branch, got, want)
	}
	entries := workJournal(t, store)
	if len(entries) != 2 || entries[0].Rig != rigWordUp || entries[1].Rig != rigWordUp || entries[0].WorkDir != slot {
		t.Errorf("journal = %+v", entries)
	}
}

// TestExecuteWorkSetupRefusals pins every way a standalone run's setup stops
// before the worker starts: the error, and no journal line.
func TestExecuteWorkSetupRefusals(t *testing.T) {
	type setup func(t *testing.T) (*storage.Store, *storage.Task, *workPlan, workOptions)
	blockBranch := func(t *testing.T, repo, branch string) {
		t.Helper()
		i := strings.Index(branch, "/")
		if i < 0 {
			t.Fatalf("branch %q has no directory to block", branch)
		}
		gitT(t, repo, "branch", branch[:i])
	}
	cases := []struct {
		name  string
		setup setup
		want  string
	}{
		{"a dirty main checkout", func(t *testing.T) (*storage.Store, *storage.Task, *workPlan, workOptions) {
			store, task, plan, opts := executorFixture(t)
			if err := os.WriteFile(filepath.Join(plan.workDir, "f.txt"), []byte("dirty\n"), 0644); err != nil {
				t.Fatal(err)
			}
			return store, task, plan, opts
		}, "is dirty - commit/stash first"},
		{"a branch git cannot check out", func(t *testing.T) (*storage.Store, *storage.Task, *workPlan, workOptions) {
			store, task, plan, opts := executorFixture(t)
			blockBranch(t, plan.workDir, plan.branch)
			return store, task, plan, opts
		}, "prepare branch "},
		{"a diverged base in a slot", func(t *testing.T) (*storage.Store, *storage.Task, *workPlan, workOptions) {
			store, task, plan, opts, repo, _ := workInSlot(t, nil)
			gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "local only")
			other := t.TempDir()
			gitT(t, other, "clone", "-q", gitReadT(t, repo, "remote", "get-url", "origin"), "c")
			c := filepath.Join(other, "c")
			gitT(t, c, "config", "user.email", "t@t")
			gitT(t, c, "config", "user.name", "t")
			gitT(t, c, "commit", "-q", "--allow-empty", "-m", "origin only again")
			gitT(t, c, "push", "-q", "origin", "main")
			return store, task, plan, opts
		}, "has diverged from origin/main"},
		{"a fresh branch git cannot create", func(t *testing.T) (*storage.Store, *storage.Task, *workPlan, workOptions) {
			store, task, plan, opts, repo, _ := workInSlot(t, nil)
			blockBranch(t, repo, plan.branch)
			return store, task, plan, opts
		}, "prepare fresh branch "},
		{"a failing prepare", func(t *testing.T) (*storage.Store, *storage.Task, *workPlan, workOptions) {
			store, task, plan, opts, _, _ := workInSlot(t, func(e *storage.Executor, _ *storage.Task) { e.Prepare = "false" })
			return store, task, plan, opts
		}, "prepare cmd (false) failed in "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeClaude(t, "echo '"+envelope(workerVerified, "unreached")+"'")
			store, task, plan, opts := c.setup(t)
			_, err := executeWork(store, task, plan, opts)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
			if entries := workJournal(t, store); len(entries) != 0 {
				t.Errorf("a refused run must journal nothing: %+v", entries)
			}
		})
	}
}

// TestExecuteWorkAllowDirtyKeepsTheChanges pins --allow-dirty in the main
// checkout: the dirty tree is not refused and survives the run untouched.
func TestExecuteWorkAllowDirtyKeepsTheChanges(t *testing.T) {
	fakeClaude(t, "echo '"+envelope(workerVerified, "all green")+"'")
	store, task, plan, opts := executorFixture(t)
	opts.allowDirty = true
	if err := os.WriteFile(filepath.Join(plan.workDir, "f.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := executeWork(store, task, plan, opts); err != nil {
		t.Fatalf("--allow-dirty must not refuse a dirty tree: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(plan.workDir, "f.txt")); string(b) != "dirty\n" {
		t.Errorf("the dirty change was lost: %q", b)
	}
	if got := gitReadT(t, plan.workDir, "rev-parse", "--abbrev-ref", "HEAD"); got != plan.branch {
		t.Errorf("checkout on %q, want %q", got, plan.branch)
	}
}
