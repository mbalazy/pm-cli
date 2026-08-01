package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// TestDriveSubParkSemantics pins the ONE piece of pm state the two epic modes
// disagree on, and the only subFlow hook whose two implementations differ
// observably: integration parks a non-green sub on `waiting`, independent
// deliberately leaves it on `doing` (a human returns to every task in a batch
// anyway, so waiting would be noise).
//
// The envelope is `failed` on purpose. A `blocked` one is parked by
// applyWorkerResult's own !independent gate - a different mechanism, already
// covered - and would never reach flow.park. Wiring independent mode to the
// integration park would corrupt every batch run, silently, without this test.
func TestDriveSubParkSemantics(t *testing.T) {
	cases := []struct {
		name        string
		independent bool
		want        storage.TaskStatus
	}{
		{"integration parks a failed sub on waiting", false, storage.StatusWaiting},
		{"independent leaves a failed sub on doing", true, storage.StatusDoing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope("failed", "verify red")+"'")
			store, sub, _, opts := executorFixture(t)
			proj, _ := store.GetProject("app")
			addTask(t, store, "app", storage.TaskMeta{ID: "app-t", Title: "Epic", Status: storage.StatusDoing}, "")
			tracker, err := store.FindTask("app", "app-t")
			if err != nil {
				t.Fatal(err)
			}

			rw := storage.NewRunWriter(store.ProjectDir("app"), &storage.RunState{
				TaskID: "app-t", Project: "app", Kind: "run-epic", Status: storage.RunStatusRunning,
				PID: os.Getpid(), Subs: []storage.SubRun{{ID: sub.Meta.ID, Status: storage.RunStatusRunning}},
			})
			opts.standalone = false
			opts.independent = tc.independent

			var oc subOutcome
			if tc.independent {
				base := gitHeadBranch(t, proj.Path)
				oc = driveSubIndependent(store, proj.Path, "app", tracker, sub, base, storage.StatusDone, opts, rw, false)
			} else {
				gitT(t, proj.Path, "checkout", "-q", "-B", "epic/app-t")
				oc = driveSub(store, proj.Path, "app", tracker, sub, "epic/app-t", storage.StatusDone, opts, rw, false)
			}

			if oc.result != "failed" {
				t.Fatalf("outcome = %+v, want a failed sub", oc)
			}
			final, err := store.FindTask("app", "app-1")
			if err != nil {
				t.Fatal(err)
			}
			if final.Meta.Status != tc.want {
				t.Fatalf("sub status = %q, want %q", final.Meta.Status, tc.want)
			}
			// Either way the reason bubbles up to the parent's Manager Notes.
			parent, _ := store.FindTask("app", "app-t")
			if !strings.Contains(parent.Body, "app-1 · failed") {
				t.Errorf("parent should carry the parked sub's note:\n%s", parent.Body)
			}
		})
	}
}

// TestDriveSubIndependentPushesOnWorkerCrash covers the crashNote hook: a dead
// worker's partial commits are pushed before the note is returned, because the
// next run wipes the branch. The integration mode has no such hook.
func TestDriveSubIndependentPushesOnWorkerCrash(t *testing.T) {
	// Commits, then dies without an envelope -> executeWork returns an error.
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\nexit 1")
	store, sub, _, opts := executorFixture(t)
	proj, _ := store.GetProject("app")
	addTask(t, store, "app", storage.TaskMeta{ID: "app-t", Title: "Batch", Status: storage.StatusDoing}, "")
	tracker, err := store.FindTask("app", "app-t")
	if err != nil {
		t.Fatal(err)
	}

	rw := storage.NewRunWriter(store.ProjectDir("app"), &storage.RunState{
		TaskID: "app-t", Project: "app", Kind: "run-epic", Status: storage.RunStatusRunning, PID: os.Getpid(),
	})
	opts.standalone = false
	opts.independent = true

	base := gitHeadBranch(t, proj.Path)
	oc := driveSubIndependent(store, proj.Path, "app", tracker, sub, base, storage.StatusDone, opts, rw, false)

	if oc.result != "failed" {
		t.Fatalf("outcome = %+v, want failed", oc)
	}
	// The fixture repo has no remote, so the push degrades to a note - which is
	// exactly what proves the crash path went through pushIfAhead at all.
	if !strings.Contains(oc.note, "no remote") {
		t.Errorf("crash note should carry the push outcome, got %q", oc.note)
	}
	// Ordering is the worker's error FIRST, then the push note (the completed-run
	// path is the other way round).
	if !strings.HasPrefix(oc.note, "claude worker failed") {
		t.Errorf("crash note should lead with the worker error, got %q", oc.note)
	}
	final, _ := store.FindTask("app", "app-1")
	if final.Meta.Status != storage.StatusDoing {
		t.Errorf("independent mode must not park a crashed sub, got %q", final.Meta.Status)
	}
}

// epicRunFixture builds a store + git repo ready for a real executeEpic run:
// the done status is `done` (in the project's default statuses) and the repo
// sits on `main`, which independent mode requires to exist.
func epicRunFixture(t *testing.T) (*storage.Store, string) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "checkout", "-q", "-B", "main")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	if err := store.CreateProject("app", &storage.Project{Name: "App", Path: repo, Executor: &storage.Executor{
		Enabled: true, StartStatus: "todo", DoneStatus: "done",
	}}); err != nil {
		t.Fatal(err)
	}
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1", Title: "Epic", Status: storage.StatusDoing}, "")
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1-1", Title: "Only Sub", Status: storage.StatusTodo, Parent: "app-1", Order: 10}, "")
	return store, repo
}

func runEpic(t *testing.T, store *storage.Store, opts epicOptions) (string, string) {
	t.Helper()
	var out, errOut strings.Builder
	opts.out, opts.errOut = &out, &errOut
	if opts.timeout == 0 {
		opts.timeout = 30 * time.Second
	}
	if opts.maxTurns == 0 {
		opts.maxTurns = 10
	}
	plan, err := planEpic(store, []string{"app", "app-1"}, opts)
	if err != nil {
		t.Fatalf("planEpic: %v", err)
	}
	if err := executeEpic(store, plan, opts); err != nil {
		t.Fatalf("executeEpic: %v\nstderr:\n%s", err, errOut.String())
	}
	return out.String(), errOut.String()
}

func gitLogAll(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--format=%s", ref).Output()
	if err != nil {
		t.Fatalf("git log %s: %v", ref, err)
	}
	return string(out)
}

// TestExecuteEpicIntegrationMode drives the extracted manager loop end to end
// against a fake worker: the sub's work must land on the integration branch and
// the sub must reach the done status.
func TestExecuteEpicIntegrationMode(t *testing.T) {
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope("merged", "all green")+"'")
	store, repo := epicRunFixture(t)

	stdout, stderr := runEpic(t, store, epicOptions{noPR: true})

	sub, _ := store.FindTask("app", "app-1-1")
	if sub.Meta.Status != storage.StatusDone {
		t.Errorf("verify-green sub must reach the done status, got %q", sub.Meta.Status)
	}
	if log := gitLogAll(t, repo, "epic/app-1"); !strings.Contains(log, "worker commit") {
		t.Errorf("the sub's work is not on the integration branch:\n%s", log)
	}
	if !strings.Contains(stdout, "# Epic app-1 summary (integration: epic/app-1)") {
		t.Errorf("summary should name the integration branch:\n%s", stdout)
	}
	if !strings.Contains(stderr, "pm run-epic: app-1 on epic/app-1") {
		t.Errorf("stderr should announce the integration run:\n%s", stderr)
	}
	// --no-pr must suppress the PR step (with no remote, openEpicPR announces
	// itself on stderr - its absence is the assertion).
	if strings.Contains(stderr, "no git remote configured") {
		t.Errorf("--no-pr must not attempt the epic PR:\n%s", stderr)
	}

	run, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil || run.Status != storage.RunStatusDone {
		t.Fatalf("run-state: %+v err=%v", run, err)
	}
	if run.CurrentSub != "" || run.CurrentSession != "" {
		t.Errorf("the loop must clear the in-flight markers, got sub=%q session=%q", run.CurrentSub, run.CurrentSession)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || entries[0].Event != "start" || entries[1].Event != "end" {
		t.Fatalf("journal must carry a start+end pair: %+v", entries)
	}
	if entries[1].Branch != "epic/app-1" {
		t.Errorf("journal branch = %q, want the integration branch", entries[1].Branch)
	}
	if len(entries[1].Subs) != 1 || entries[1].Subs[0].Result != "merged" {
		t.Fatalf("journal end must record the merged sub: %+v", entries[1].Subs)
	}
}

// TestExecuteEpicIndependentMode: the same loop in batch mode must NOT create
// an integration branch and must NOT merge anything - only move the green sub
// to the done status and report against the fork base.
func TestExecuteEpicIndependentMode(t *testing.T) {
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope("merged", "all green")+"'")
	store, repo := epicRunFixture(t)

	stdout, _ := runEpic(t, store, epicOptions{independent: true})

	sub, _ := store.FindTask("app", "app-1-1")
	if sub.Meta.Status != storage.StatusDone {
		t.Errorf("verify-green sub must reach the done status, got %q", sub.Meta.Status)
	}
	if branchExists(repo, "epic/app-1") {
		t.Error("independent mode must not create an integration branch")
	}
	if log := gitLogAll(t, repo, "main"); strings.Contains(log, "worker commit") {
		t.Errorf("independent mode must not merge into the base:\n%s", log)
	}
	if !strings.Contains(stdout, "# Epic app-1 summary (independent, off main)") {
		t.Errorf("summary should name the fork base, not an integration branch:\n%s", stdout)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || entries[1].Branch != "independent:main" {
		t.Fatalf("journal must record the fork base for a batch run: %+v", entries)
	}
	if !entries[1].Independent {
		t.Error("journal end must flag the run independent")
	}
}

// TestExecuteEpicOpensThePRWhenNotSuppressed is the other half of the --no-pr
// assertion above: without the flag the PR step runs (and, with no remote,
// says so) - so an inverted flag check fails one of the two.
func TestExecuteEpicOpensThePRWhenNotSuppressed(t *testing.T) {
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope("merged", "all green")+"'")
	store, _ := epicRunFixture(t)

	_, stderr := runEpic(t, store, epicOptions{})

	if !strings.Contains(stderr, "no git remote configured") {
		t.Errorf("without --no-pr the epic PR step must run:\n%s", stderr)
	}
}

// TestExecuteEpicRejectsAnUnlistedDoneStatus: the hard gate deliberately lives
// in executeEpic, not planEpic (--dry-run has always printed the plan
// regardless). Failing the merged-move AFTER the merge landed would desync the
// re-entrant done check and re-drive the sub on the next run.
func TestExecuteEpicRejectsAnUnlistedDoneStatus(t *testing.T) {
	store, _ := epicRunFixture(t)
	if _, err := store.MutateProject("app", func(p *storage.Project) error {
		p.Executor.DoneStatus = "merged" // not in the project's default statuses
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	opts := epicOptions{noPR: true, timeout: 30 * time.Second, maxTurns: 10}
	plan, err := planEpic(store, []string{"app", "app-1"}, opts)
	if err != nil {
		t.Fatalf("planEpic must still plan (the gate is executeEpic's): %v", err)
	}
	err = executeEpic(store, plan, opts)
	if err == nil || !strings.Contains(err.Error(), "not in project app statuses") {
		t.Fatalf("expected the done-status gate to refuse, got %v", err)
	}
	// Refused before anything ran.
	sub, _ := store.FindTask("app", "app-1-1")
	if sub.Meta.Status != storage.StatusTodo {
		t.Errorf("the gate must refuse before driving a sub, got %q", sub.Meta.Status)
	}
}
