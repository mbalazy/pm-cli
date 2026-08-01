package cmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// testExecutor is the minimal executor block a manager run needs. The block
// round-trips through project.yaml (CreateProject -> ReadProject ->
// Executor.UnmarshalYAML overlays defaultExecutor()), so these statuses would
// resolve to the same values if omitted - they are spelled out so a test
// asserting on them reads without that indirection.
func testExecutor() *storage.Executor {
	return &storage.Executor{Enabled: true, StartStatus: "todo", DoneStatus: "merged"}
}

// epicFixture creates a store with one git-backed project ("app") holding a
// tracker plus subs, and returns the store. The subs' Orders deliberately
// CONTRADICT their alphabetical IDs (app-1-1 is last), so a comparator that
// dropped the Order key and fell back to the ID tiebreaker fails the
// sequencing assertion instead of passing by coincidence.
func epicFixture(t *testing.T, exc *storage.Executor) (*storage.Store, string) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	if err := store.CreateProject("app", &storage.Project{Name: "App", Path: repo, Executor: exc}); err != nil {
		t.Fatal(err)
	}
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1", Title: "Epic", Status: storage.StatusDoing}, "")
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1-1", Title: "Last", Status: storage.StatusTodo, Parent: "app-1", Order: 30}, "")
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1-3", Title: "Middle", Status: storage.StatusTodo, Parent: "app-1", Order: 20}, "")
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1-2", Title: "First", Status: storage.StatusWaiting, Parent: "app-1", Order: 10}, "")
	return store, repo
}

func subIDs(subs []*storage.Task) []string {
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = s.Meta.ID
	}
	return out
}

// TestPlanEpicPreflightErrors covers the four refusals the manager owes the
// user BEFORE anything is touched. They used to live inside a 316-line RunE
// closure and were unreachable from a test.
func TestPlanEpicPreflightErrors(t *testing.T) {
	t.Run("project without a path", func(t *testing.T) {
		store := &storage.Store{Root: t.TempDir()}
		if err := store.CreateProject("app", &storage.Project{Name: "App", Executor: testExecutor()}); err != nil {
			t.Fatal(err)
		}
		addTask(t, store, "app", storage.TaskMeta{ID: "app-1", Title: "Epic", Status: storage.StatusDoing}, "")
		_, err := planEpic(store, []string{"app", "app-1"}, epicOptions{})
		if err == nil || !strings.Contains(err.Error(), "has no path") {
			t.Fatalf("expected a no-path error, got %v", err)
		}
	})

	t.Run("project path is not a git repo", func(t *testing.T) {
		store := &storage.Store{Root: t.TempDir()}
		if err := store.CreateProject("app", &storage.Project{Name: "App", Path: t.TempDir(), Executor: testExecutor()}); err != nil {
			t.Fatal(err)
		}
		addTask(t, store, "app", storage.TaskMeta{ID: "app-1", Title: "Epic", Status: storage.StatusDoing}, "")
		_, err := planEpic(store, []string{"app", "app-1"}, epicOptions{})
		if err == nil || !strings.Contains(err.Error(), "not a git repository") {
			t.Fatalf("expected a not-a-git-repo error, got %v", err)
		}
	})

	t.Run("executor disabled", func(t *testing.T) {
		store, _ := epicFixture(t, &storage.Executor{Enabled: false})
		_, err := planEpic(store, []string{"app", "app-1"}, epicOptions{})
		if err == nil || !strings.Contains(err.Error(), "executor disabled") {
			t.Fatalf("expected an executor-disabled error, got %v", err)
		}
	})

	t.Run("--additional without configured slots", func(t *testing.T) {
		store, _ := epicFixture(t, testExecutor())
		_, err := planEpic(store, []string{"app", "app-1"}, epicOptions{additional: true})
		if err == nil || !strings.Contains(err.Error(), "no worktree slots configured") {
			t.Fatalf("expected a no-slots error, got %v", err)
		}
	})

	t.Run("task with no children is not a tracker", func(t *testing.T) {
		store, _ := epicFixture(t, testExecutor())
		addTask(t, store, "app", storage.TaskMeta{ID: "app-9", Title: "Lonely", Status: storage.StatusTodo}, "")
		_, err := planEpic(store, []string{"app", "app-9"}, epicOptions{})
		if err == nil || !strings.Contains(err.Error(), "is not a tracker") {
			t.Fatalf("expected a not-a-tracker error, got %v", err)
		}
	})

	t.Run("invalid epic_mode on the tracker", func(t *testing.T) {
		store, _ := epicFixture(t, testExecutor())
		// Written straight to disk: storage validates epic_mode on write, so a
		// bad value can only reach the manager via a hand-edited file.
		tracker, err := store.FindTask("app", "app-1")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(tracker.FilePath)
		if err != nil {
			t.Fatal(err)
		}
		corrupted := strings.Replace(string(raw), "---\n", "---\nepic_mode: sideways\n", 1)
		if err := os.WriteFile(tracker.FilePath, []byte(corrupted), 0644); err != nil {
			t.Fatal(err)
		}
		_, err = planEpic(store, []string{"app", "app-1"}, epicOptions{})
		if err == nil || !strings.Contains(err.Error(), "epic_mode") {
			t.Fatalf("expected an epic_mode validation error, got %v", err)
		}
	})
}

// TestPlanEpicResolvesSubsAndBranches: the plan sequences every child by Order
// (regardless of readiness - the per-sub gate is classifySub's job) and resolves
// the branches the run will use.
func TestPlanEpicResolvesSubsAndBranches(t *testing.T) {
	store, _ := epicFixture(t, testExecutor())

	plan, err := planEpic(store, []string{"app", "app-1"}, epicOptions{})
	if err != nil {
		t.Fatalf("planEpic: %v", err)
	}
	// By Order (10, 20, 30), NOT by ID - the two disagree in this fixture.
	if got := strings.Join(subIDs(plan.subs), ","); got != "app-1-2,app-1-3,app-1-1" {
		t.Errorf("subs = %s, want them sorted by Order", got)
	}
	if plan.epicBranch != "epic/app-1" {
		t.Errorf("epicBranch = %q", plan.epicBranch)
	}
	if plan.baseBranch != "main" {
		t.Errorf("baseBranch = %q, want the main fallback", plan.baseBranch)
	}
	if plan.startStatus != "todo" || plan.doneStatus != "merged" {
		t.Errorf("statuses = %q/%q", plan.startStatus, plan.doneStatus)
	}
	if plan.independent {
		t.Error("a tracker without epic_mode must plan as an integration run")
	}
	if plan.workDir != plan.proj.Path {
		t.Errorf("workDir = %q, want the main checkout %q", plan.workDir, plan.proj.Path)
	}

	// The plan feeds the gate: only the ready sub is driven, the waiting one is
	// skipped without a worker.
	byID := map[string]*storage.Task{}
	for _, s := range plan.subs {
		byID[s.Meta.ID] = s
	}
	want := map[string]bool{"app-1-1": true, "app-1-2": false, "app-1-3": true}
	for _, s := range plan.subs {
		_, drive, _ := classifySub(s, byID, plan.startStatus, plan.doneStatus)
		if drive != want[s.Meta.ID] {
			t.Errorf("classifySub(%s) drive = %v, want %v", s.Meta.ID, drive, want[s.Meta.ID])
		}
	}
}

func TestPlanEpicIndependentMode(t *testing.T) {
	t.Run("tracker frontmatter enables it without a flag", func(t *testing.T) {
		store, _ := epicFixture(t, testExecutor())
		tracker, _ := store.FindTask("app", "app-1")
		tracker.Meta.EpicMode = storage.EpicModeIndependent
		if err := store.WriteTask(tracker); err != nil {
			t.Fatal(err)
		}
		plan, err := planEpic(store, []string{"app", "app-1"}, epicOptions{})
		if err != nil {
			t.Fatalf("planEpic: %v", err)
		}
		if !plan.independent {
			t.Error("epic_mode: independent on the tracker must plan as a batch run")
		}
	})

	t.Run("--independent overrides a tracker without it", func(t *testing.T) {
		store, _ := epicFixture(t, testExecutor())
		plan, err := planEpic(store, []string{"app", "app-1"}, epicOptions{independent: true})
		if err != nil {
			t.Fatalf("planEpic: %v", err)
		}
		if !plan.independent {
			t.Error("--independent must plan as a batch run")
		}
	})
}

// TestPlanEpicBasePrecedence pins the deliberately asymmetric rule: in DEFAULT
// mode executor.base_branch is not consulted at all (pre-worktree behaviour),
// while --additional and independent mode both honour it - and --base always
// wins.
func TestPlanEpicBasePrecedence(t *testing.T) {
	exc := func() *storage.Executor {
		e := testExecutor()
		e.BaseBranch = "development"
		// A slot, so the --additional case gets past planEpic's no-slots refusal.
		e.Worktrees = []storage.WorktreeSlot{{Path: "../app-additional"}}
		return e
	}
	cases := []struct {
		name string
		opts epicOptions
		want string
	}{
		{"default mode ignores executor.base_branch", epicOptions{}, "main"},
		{"--base wins in default mode", epicOptions{base: "release/1"}, "release/1"},
		{"--additional honours executor.base_branch", epicOptions{additional: true}, "development"},
		{"independent mode honours executor.base_branch", epicOptions{independent: true}, "development"},
		{"--base wins over executor.base_branch", epicOptions{independent: true, base: "release/1"}, "release/1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := epicFixture(t, exc())
			plan, err := planEpic(store, []string{"app", "app-1"}, tc.opts)
			if err != nil {
				t.Fatalf("planEpic: %v", err)
			}
			if plan.baseBranch != tc.want {
				t.Errorf("baseBranch = %q, want %q", plan.baseBranch, tc.want)
			}
		})
	}
}

// TestPlanEpicHasNoSideEffects is the property the split exists for: planning a
// run must not create a branch, move a sub, or write a run-state/journal. It is
// what lets --dry-run print the real plan and what makes the preflight above
// safe to test.
func TestPlanEpicHasNoSideEffects(t *testing.T) {
	store, repo := epicFixture(t, testExecutor())

	branchesBefore := gitBranches(t, repo)
	headBefore := gitHead(t, repo)
	statusesBefore := subStatuses(t, store)

	if _, err := planEpic(store, []string{"app", "app-1"}, epicOptions{}); err != nil {
		t.Fatalf("planEpic: %v", err)
	}

	if got := gitBranches(t, repo); got != branchesBefore {
		t.Errorf("planEpic created/removed branches: %q -> %q", branchesBefore, got)
	}
	if got := gitHead(t, repo); got != headBefore {
		t.Errorf("planEpic moved HEAD: %q -> %q", headBefore, got)
	}
	if got := subStatuses(t, store); got != statusesBefore {
		t.Errorf("planEpic moved subs: %q -> %q", statusesBefore, got)
	}
	execDir := filepath.Join(store.ProjectDir("app"), ".executor")
	if _, err := os.Stat(execDir); !os.IsNotExist(err) {
		t.Errorf("planEpic wrote executor run state at %s (err=%v)", execDir, err)
	}
}

func gitBranches(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "branch", "--list", "--format=%(refname:short)").Output()
	if err != nil {
		t.Fatalf("git branch: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func subStatuses(t *testing.T, store *storage.Store) string {
	t.Helper()
	tasks, err := store.GetTasks("app")
	if err != nil {
		t.Fatalf("GetTasks: %v", err)
	}
	var lines []string
	for _, task := range tasks {
		lines = append(lines, task.Meta.ID+"="+string(task.Meta.Status))
	}
	sort.Strings(lines)
	return strings.Join(lines, ",")
}

// TestApplyWorkerResultRefusesToResurrectADeletedTask: the executor holds its
// task copy across a 30+ minute worker run. If the task is deleted meanwhile
// (a parallel pm_delete_task), the fresh re-read misses - and writing the copy
// in hand would recreate the file, complete with a worker brief nobody will
// ever go looking for. The write must be refused instead.
func TestApplyWorkerResultRefusesToResurrectADeletedTask(t *testing.T) {
	for _, status := range []string{"merged", "blocked"} {
		t.Run("worker "+status, func(t *testing.T) {
			store := &storage.Store{Root: t.TempDir()}
			if err := store.CreateProject("app", &storage.Project{Name: "App"}); err != nil {
				t.Fatal(err)
			}
			if err := store.AddTask("app", &storage.Task{
				Meta: storage.TaskMeta{ID: "app-1", Title: "T", Status: storage.StatusDoing},
			}); err != nil {
				t.Fatal(err)
			}
			stale, err := store.FindTask("app", "app-1")
			if err != nil {
				t.Fatal(err)
			}

			// "Another session" deletes the task while the worker runs.
			if err := store.DeleteTask(stale); err != nil {
				t.Fatal(err)
			}

			res := &workerResult{Status: status, Summary: "done", Branch: "feat/x", Commits: []string{"abc"}}
			err = applyWorkerResult(io.Discard, store, stale, "feat/x", "sess-1", res, true, false)
			if err == nil {
				t.Fatal("writing a worker result onto a deleted task must fail, not resurrect it")
			}
			if !strings.Contains(err.Error(), "app-1") {
				t.Errorf("error should name the task, got: %v", err)
			}
			if _, statErr := os.Stat(stale.FilePath); !os.IsNotExist(statErr) {
				t.Fatalf("the deleted task file is back at %s (err=%v)", stale.FilePath, statErr)
			}
			if found, findErr := store.FindTask("app", "app-1"); findErr == nil {
				t.Fatalf("deleted task resurrected: %+v", found.Meta)
			}
		})
	}
}

// TestRecordSubFeedbackRefusesToResurrectADeletedParent: same hazard on the
// manager side - the parent tracker is read once at run start and rewritten
// after EVERY sub, so a tracker deleted mid-run used to come back from a stale
// pointer.
func TestRecordSubFeedbackRefusesToResurrectADeletedParent(t *testing.T) {
	store, slug := tempStore(t)
	parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-20", Title: "Epic", Status: storage.StatusDoing},
		storage.SpecStart+"\n## Description\nbuild it\n"+storage.SpecEnd)

	if err := store.DeleteTask(parent); err != nil {
		t.Fatal(err)
	}
	filePath := parent.FilePath

	err := recordSubFeedback(store, parent, "proj-20-1", "blocked", []string{"needs a decision"})
	if err == nil {
		t.Fatal("recording feedback onto a deleted parent must fail, not resurrect it")
	}
	if !strings.Contains(err.Error(), "proj-20") {
		t.Errorf("error should name the parent, got: %v", err)
	}
	if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
		t.Fatalf("the deleted parent file is back at %s (err=%v)", filePath, statErr)
	}
}
