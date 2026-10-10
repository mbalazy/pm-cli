package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// greenWorker is the fake worker of the tests below: one commit, a green
// envelope.
var greenWorker = "git commit -q --allow-empty -m 'worker commit'\necho '" + envelope("merged", "all green") + "'"

func mutateExecutor(t *testing.T, store *storage.Store, fn func(e *storage.Executor)) {
	t.Helper()
	if _, err := store.MutateProject("app", func(p *storage.Project) error {
		fn(p.Executor)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func journalEntries(t *testing.T, store *storage.Store) []storage.JournalEntry {
	t.Helper()
	entries, err := storage.ReadJournal(store.ProjectDir("app"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// divergeLocalMain gives the clone's local main a commit origin lacks, on top
// of the fixture's origin commit the clone lacks.
func divergeLocalMain(t *testing.T, repo string) {
	t.Helper()
	gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "local only")
	gitT(t, repo, "fetch", "-q", "origin")
}

// TestExecuteEpicSetupRefusals pins every way the run's setup stops before a
// sub is touched: the error, and that no sub moved and no journal line exists.
func TestExecuteEpicSetupRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) (*storage.Store, epicOptions)
		want  string
	}{
		{"a dirty main checkout", func(t *testing.T) (*storage.Store, epicOptions) {
			store, repo := epicRunFixture(t)
			if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("dirty\n"), 0644); err != nil {
				t.Fatal(err)
			}
			return store, epicOptions{noPR: true}
		}, "is dirty - commit/stash first"},
		{"an integration branch git cannot create", func(t *testing.T) (*storage.Store, epicOptions) {
			store, repo := epicRunFixture(t)
			gitT(t, repo, "branch", "epic") // a branch named epic blocks every epic/<id>
			return store, epicOptions{noPR: true}
		}, "create integration branch epic/app-1"},
		{"a diverged base for a fresh integration branch", func(t *testing.T) (*storage.Store, epicOptions) {
			store, repo, _ := epicSlotFixture(t)
			divergeLocalMain(t, repo)
			return store, epicOptions{noPR: true}
		}, "has diverged from origin/main"},
		{"a diverged base in independent mode", func(t *testing.T) (*storage.Store, epicOptions) {
			store, repo, _ := epicSlotFixture(t)
			divergeLocalMain(t, repo)
			return store, epicOptions{independent: true}
		}, "independent mode: base branch main has diverged"},
		{"a pinned slot that does not exist", func(t *testing.T) (*storage.Store, epicOptions) {
			store, _, _ := epicSlotFixture(t)
			return store, epicOptions{independent: true, additional: true, slotPin: 5}
		}, "--slot 5"},
		{"a failing prepare", func(t *testing.T) (*storage.Store, epicOptions) {
			store, _, _ := epicSlotFixture(t)
			mutateExecutor(t, store, func(e *storage.Executor) { e.Prepare = "false" })
			return store, epicOptions{independent: true, additional: true}
		}, "prepare cmd (false) failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeClaude(t, "echo '"+envelope("merged", "unreached")+"'")
			store, opts := c.setup(t)
			err := runEpicErr(t, store, opts)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
			if sub, _ := store.FindTask("app", "app-1-1"); sub.Meta.Status != storage.StatusTodo {
				t.Errorf("app-1-1 moved to %q", sub.Meta.Status)
			}
			if entries := journalEntries(t, store); len(entries) != 0 {
				t.Errorf("a refused run must journal nothing: %+v", entries)
			}
		})
	}
}

// TestExecuteEpicSubsItDoesNotDrive pins the loop's no-worker paths and the
// per-sub model override: a finished sub starts as skipped in the run-state, a
// manual sub is announced and recorded without a worker, and a sub's own model
// reaches its worker's argv.
func TestExecuteEpicSubsItDoesNotDrive(t *testing.T) {
	argvLog := filepath.Join(t.TempDir(), "argv")
	t.Setenv("PM_TEST_ARGV_LOG", argvLog)
	fakeClaude(t, `echo "$@" >> "$PM_TEST_ARGV_LOG"`+"\n"+greenWorker)
	store, _ := epicRunFixture(t)
	sub1, _ := store.FindTask("app", "app-1-1")
	sub1.Meta.Model = "sonnet"
	if err := store.WriteTask(sub1); err != nil {
		t.Fatal(err)
	}
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1-2", Title: "Finished Sub", Status: storage.StatusDone, Parent: "app-1", Order: 20}, "")
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1-3", Title: "Manual Sub", Status: storage.StatusTodo, Parent: "app-1", Order: 30, Mode: "manual"}, "")

	_, stderr := runEpic(t, store, epicOptions{noPR: true})

	if !strings.Contains(stderr, "pm run-epic: app-1-1 model override -> sonnet") {
		t.Errorf("the override must be announced:\n%s", stderr)
	}
	argv, _ := os.ReadFile(argvLog)
	if !strings.Contains(string(argv), "--model sonnet") {
		t.Errorf("the worker did not get the sub's model: %s", argv)
	}
	if !strings.Contains(stderr, "pm run-epic: app-1-3") {
		t.Errorf("the manual sub must be announced:\n%s", stderr)
	}
	if s, _ := store.FindTask("app", "app-1-3"); s.Meta.Status != storage.StatusTodo {
		t.Errorf("a manual sub's status is the human's, got %q", s.Meta.Status)
	}

	run, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range run.Subs {
		got[s.ID] = s.Status
	}
	if got["app-1-1"] != subMerged || got["app-1-2"] != subSkipped || got["app-1-3"] != subManual {
		t.Errorf("run-state subs = %v", got)
	}
	end := journalEntries(t, store)[1]
	results := map[string]string{}
	for _, s := range end.Subs {
		results[s.ID] = s.Result
	}
	if results["app-1-1"] != subMerged || results["app-1-2"] != subSkipped || results["app-1-3"] != subManual {
		t.Errorf("journal subs = %v", results)
	}
}

// TestExecuteEpicIntegrationBaseline pins the baseline step of an integration
// run: captured on the integration branch, named in the journal.
func TestExecuteEpicIntegrationBaseline(t *testing.T) {
	fakeClaude(t, greenWorker)
	store, _ := epicRunFixture(t)
	mutateExecutor(t, store, func(e *storage.Executor) { e.Baseline = "echo baseline-ran" })

	_, stderr := runEpic(t, store, epicOptions{noPR: true})

	if !strings.Contains(stderr, "pm run-epic: baseline in ") {
		t.Errorf("the baseline must run:\n%s", stderr)
	}
	entries := journalEntries(t, store)
	if entries[0].Baseline != "echo baseline-ran" || entries[1].Baseline != "echo baseline-ran" {
		t.Errorf("journal baseline = %q / %q", entries[0].Baseline, entries[1].Baseline)
	}
}

// TestExecuteEpicSlotSetup pins the slot-only steps that print or record
// something: the claimed-slot line with more than one slot, the rig check for a
// runtime sub, and a baseline whose checkout fails.
func TestExecuteEpicSlotSetup(t *testing.T) {
	t.Run("several slots name the one claimed", func(t *testing.T) {
		fakeClaude(t, greenWorker)
		store, _, slot := epicSlotFixture(t)
		mutateExecutor(t, store, func(e *storage.Executor) {
			e.Worktrees = append(e.Worktrees, storage.WorktreeSlot{Path: slot + "-two"})
		})
		_, stderr := runEpic(t, store, epicOptions{independent: true, additional: true})
		if !strings.Contains(stderr, "pm run-epic: claimed worktree slot "+slot+"\n") {
			t.Errorf("the claimed slot must be named:\n%s", stderr)
		}
	})

	t.Run("a runtime sub gets the rig checked once", func(t *testing.T) {
		fakeClaude(t, greenWorker)
		store, _, _ := epicSlotFixture(t)
		mutateExecutor(t, store, func(e *storage.Executor) { e.Rig = "true" })
		sub, _ := store.FindTask("app", "app-1-2")
		sub.Meta.Runtime = storage.RuntimeOn
		if err := store.WriteTask(sub); err != nil {
			t.Fatal(err)
		}
		_, stderr := runEpic(t, store, epicOptions{independent: true, additional: true})
		if strings.Count(stderr, "pm run-epic: rig check for 1 runtime sub(s) in ") != 1 {
			t.Errorf("the rig must be checked once:\n%s", stderr)
		}
		if entries := journalEntries(t, store); entries[0].Rig != rigWordUp || entries[1].Rig != rigWordUp {
			t.Errorf("journal rig = %q / %q", entries[0].Rig, entries[1].Rig)
		}
	})

	t.Run("no runtime sub leaves the rig alone", func(t *testing.T) {
		fakeClaude(t, greenWorker)
		store, _, _ := epicSlotFixture(t)
		mutateExecutor(t, store, func(e *storage.Executor) { e.Rig = "true" })
		_, stderr := runEpic(t, store, epicOptions{independent: true, additional: true})
		if strings.Contains(stderr, "rig check") || journalEntries(t, store)[0].Rig != "" {
			t.Errorf("the rig ran with no runtime sub:\n%s", stderr)
		}
	})

	t.Run("a baseline whose checkout fails is skipped", func(t *testing.T) {
		fakeClaude(t, greenWorker)
		store, _, _ := epicSlotFixture(t)
		// prepare runs after the fork ref is checked out and before the
		// baseline checks it out again - deleting it there fails that checkout.
		mutateExecutor(t, store, func(e *storage.Executor) { e.Prepare = "git update-ref -d refs/remotes/origin/main" })
		_, stderr := runEpic(t, store, epicOptions{independent: true, additional: true})
		if !strings.Contains(stderr, "pm run-epic: checkout origin/main for baseline failed (") ||
			!strings.Contains(stderr, ") - continuing without a baseline") {
			t.Errorf("the failed checkout must be named:\n%s", stderr)
		}
		if b := journalEntries(t, store)[0].Baseline; b != "" {
			t.Errorf("journal baseline = %q, want none", b)
		}
	})
}

// TestExecuteEpicIndependentChainFreesTheLastBranch pins what an independent
// run does right before it chains the acceptance: the main checkout goes back
// to the base, a slot detaches at the fork ref, so the last sub's branch is
// free for the acceptance to open.
func TestExecuteEpicIndependentChainFreesTheLastBranch(t *testing.T) {
	t.Run("main checkout", func(t *testing.T) {
		fakeClaude(t, greenWorker)
		store, repo := epicRunFixture(t)
		setTrackerFinishMode(t, store, storage.FinishModeAuto)
		fakePM(t, `echo "argv: $@"`)
		_, stderr := runEpic(t, store, epicOptions{independent: true})
		waitForFile(t, storage.FinishRunLogPath(store.ProjectDir("app"), "app-1"))
		if got := gitReadT(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
			t.Errorf("main checkout left on %q, want main", got)
		}
		if !strings.Contains(stderr, "chained the acceptance of app-1") {
			t.Errorf("the run must chain:\n%s", stderr)
		}
	})

	t.Run("slot", func(t *testing.T) {
		fakeClaude(t, greenWorker)
		store, repo, slot := epicSlotFixture(t)
		setTrackerFinishMode(t, store, storage.FinishModeAuto)
		fakePM(t, `echo "argv: $@"`)
		runEpic(t, store, epicOptions{independent: true, additional: true})
		waitForFile(t, storage.FinishRunLogPath(store.ProjectDir("app"), "app-1"))
		if got := gitReadT(t, slot, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
			t.Errorf("slot left on %q, want a detached HEAD", got)
		}
		if got, want := gitReadT(t, slot, "rev-parse", "HEAD"), gitReadT(t, repo, "rev-parse", "origin/main"); got != want {
			t.Errorf("slot detached at %s, want origin/main %s", got, want)
		}
	})
}

// headTo is a shell step that appends the branch checked out where it runs
// (or HEAD when detached) to file.
func headTo(file string) string {
	return "git rev-parse --abbrev-ref HEAD >> " + file
}

// TestExecuteEpicIntegrationInASlot pins an integration run inside a claimed
// slot: prepare and baseline run on the integration branch (never detached),
// the worker runs in the slot with the slot's env, and the slot ends on the
// integration branch.
func TestExecuteEpicIntegrationInASlot(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	t.Setenv("PM_TEST_SEEN", seen)
	fakeClaude(t, `echo "worker $(pwd -P) $PM_TEST_SLOT_VAR" >> "$PM_TEST_SEEN"`+"\n"+greenWorker)
	store, repo, slot := epicSlotFixture(t)
	// The fixture's local main is a commit behind origin; catch it up so the
	// fresh integration branch has nothing to fast-forward.
	gitT(t, repo, "pull", "-q", "--ff-only")
	mutateExecutor(t, store, func(e *storage.Executor) {
		e.Prepare = headTo(seen)
		e.Baseline = headTo(seen)
		e.Worktrees[0].Env = map[string]string{"PM_TEST_SLOT_VAR": "slot-env"}
	})

	_, stderr := runEpic(t, store, epicOptions{additional: true, noPR: true})

	if !strings.Contains(stderr, "pm run-epic: app-1 on epic/app-1 (2 sub(s))") {
		t.Errorf("the run must announce the integration branch:\n%s", stderr)
	}
	data, _ := os.ReadFile(seen)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	slotReal, _ := filepath.EvalSymlinks(slot)
	want := []string{"epic/app-1", "epic/app-1", "worker " + slotReal + " slot-env", "worker " + slotReal + " slot-env"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("prepare, baseline and workers saw %q, want %q", lines, want)
	}
	if got := gitReadT(t, slot, "rev-parse", "--abbrev-ref", "HEAD"); got != "epic/app-1" {
		t.Errorf("slot left on %q, want epic/app-1", got)
	}
	if got := gitReadT(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("main checkout moved to %q", got)
	}
	if log := gitLogAll(t, slot, "epic/app-1"); strings.Count(log, "worker commit") != 2 {
		t.Errorf("both subs' work must be on the integration branch:\n%s", log)
	}
	if b := journalEntries(t, store)[0].Baseline; b != headTo(seen) {
		t.Errorf("journal baseline = %q", b)
	}
}

// TestExecuteEpicIndependentBaselineInTheMainCheckout pins where an
// independent run in the main checkout takes its baseline: on the base
// branch, checked out by name.
func TestExecuteEpicIndependentBaselineInTheMainCheckout(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "seen")
	fakeClaude(t, greenWorker)
	store, _ := epicRunFixture(t)
	mutateExecutor(t, store, func(e *storage.Executor) { e.Baseline = headTo(seen) })

	_, stderr := runEpic(t, store, epicOptions{independent: true})

	if !strings.Contains(stderr, "pm run-epic: baseline in ") {
		t.Errorf("the baseline must run:\n%s", stderr)
	}
	if data, _ := os.ReadFile(seen); strings.TrimSpace(string(data)) != "main" {
		t.Errorf("baseline ran on %q, want main", data)
	}
	if b := journalEntries(t, store)[0].Baseline; b != headTo(seen) {
		t.Errorf("journal baseline = %q", b)
	}
}

// TestExecuteEpicBaselineThatTimesOut pins a baseline that ran but produced
// nothing usable: the run goes on and the journal names no baseline.
func TestExecuteEpicBaselineThatTimesOut(t *testing.T) {
	orig := baselineTimeout
	baselineTimeout = 200 * time.Millisecond
	t.Cleanup(func() { baselineTimeout = orig })
	fakeClaude(t, greenWorker)
	store, _ := epicRunFixture(t)
	mutateExecutor(t, store, func(e *storage.Executor) { e.Baseline = "sleep 5" })

	_, stderr := runEpic(t, store, epicOptions{noPR: true})

	if !strings.Contains(stderr, "baseline cmd timed out") {
		t.Errorf("the timeout must be named:\n%s", stderr)
	}
	entries := journalEntries(t, store)
	if entries[0].Baseline != "" || entries[1].Baseline != "" {
		t.Errorf("journal baseline = %q / %q, want none", entries[0].Baseline, entries[1].Baseline)
	}
	if sub, _ := store.FindTask("app", "app-1-1"); sub.Meta.Status != storage.StatusDone {
		t.Errorf("the run must go on without a baseline, sub on %q", sub.Meta.Status)
	}
}
