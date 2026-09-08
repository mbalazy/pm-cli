package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
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
	// pm-cli-75: this worker made progress (one commit) before dying, so the
	// note must NOT carry the zero-progress tag - that is reserved for a
	// worker that never got that far.
	if strings.HasPrefix(oc.note, zeroProgressPrefix) {
		t.Errorf("a sub that made progress must not carry the zero-progress prefix, got %q", oc.note)
	}
	final, _ := store.FindTask("app", "app-1")
	if final.Meta.Status != storage.StatusDoing {
		t.Errorf("independent mode must not park a crashed sub, got %q", final.Meta.Status)
	}
}

// TestZeroProgressCrashNote covers the pm-cli-75 tag: a worker that dies
// before committing anything gets a distinguishing prefix - the "made
// progress" side of the contrast is TestDriveSubIndependentPushesOnWorkerCrash
// above, which asserts the prefix is ABSENT there.
func TestZeroProgressCrashNote(t *testing.T) {
	fakeClaude(t, "exit 1") // dies immediately, nothing committed
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

	if oc.result != subFailed {
		t.Fatalf("outcome = %+v, want failed", oc)
	}
	if !strings.HasPrefix(oc.note, zeroProgressPrefix) {
		t.Errorf("zero-progress note should carry the prefix, got %q", oc.note)
	}
}

// TestZeroProgressCrashNoteAheadCheckError covers the other half of the "err
// == nil && ahead == 0" guard: when the ahead-check ITSELF fails (a base ref
// that stops resolving mid-run), that must not read as zero progress - the
// same "when in doubt, don't claim silence" rule pushIfAhead already applies
// to whether to push. The fake worker deletes the base branch ref before
// dying, so gitAheadCount(dir, branch, base) errors on a missing ref.
func TestZeroProgressCrashNoteAheadCheckError(t *testing.T) {
	store, sub, _, opts := executorFixture(t)
	proj, _ := store.GetProject("app")
	base := gitHeadBranch(t, proj.Path) // whatever git's default init branch is named
	// The fake worker deletes the base ref (not currently checked out - the
	// sub's own branch is) and then dies, so the manager's post-mortem
	// gitAheadCount(dir, sub-branch, base) fails to resolve it.
	fakeClaude(t, "git branch -D "+base+" 2>/dev/null\nexit 1")
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

	oc := driveSubIndependent(store, proj.Path, "app", tracker, sub, base, storage.StatusDone, opts, rw, false)

	if oc.result != subFailed {
		t.Fatalf("outcome = %+v, want failed", oc)
	}
	if strings.HasPrefix(oc.note, zeroProgressPrefix) {
		t.Errorf("a failed ahead-check must not read as zero progress, got %q", oc.note)
	}
}

// epicRunFixture builds a store + git repo ready for a real executeEpic run:
// the done status is `done` (in the project's default statuses) and the repo
// sits on `main`, which independent mode requires to exist.
func epicRunFixture(t *testing.T) (*storage.Store, string) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	repo := t.TempDir()
	gitInitRepo(t, repo)
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

// TestExecuteEpicSummaryWarnsOfRerunSkip is the end-to-end wiring proof for
// rerunSkips: a real executeEpic run whose only sub crashes without going
// green leaves it on `doing` (independent mode never parks), off the `todo`
// start status - exactly the reporter's "not ready (status doing)" silence
// (pm-cli-75) - and the printed summary must say so.
func TestExecuteEpicSummaryWarnsOfRerunSkip(t *testing.T) {
	fakeClaude(t, "exit 1") // dies with no envelope, no commit
	store, _ := epicRunFixture(t)

	stdout, _ := runEpic(t, store, epicOptions{independent: true})

	sub, _ := store.FindTask("app", "app-1-1")
	if sub.Meta.Status != storage.StatusDoing {
		t.Fatalf("crashed sub should stay on doing in independent mode, got %q", sub.Meta.Status)
	}
	if !strings.Contains(stdout, "A re-run will NOT pick up: app-1-1 (status doing)") {
		t.Errorf("summary should warn that a plain re-run skips the stuck sub:\n%s", stdout)
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

// TestExecuteEpicRejectsAnUnlistedDoneStatus: the hard gate refuses before
// anything is touched, because failing the merged-move AFTER the merge landed
// would desync the re-entrant done check and re-drive the sub on the next run.
//
// planEpic resolves the verdict (so --dry-run reaches the same one - see
// TestDryRunAppliesTheDoneStatusGate) but must still PLAN: the dry-run's whole
// job is to print what it resolved before reporting the blocker.
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

// The headline of the honest-status change, end to end: a green batch sub lands
// on the independent status and is RECORDED as pushed, in the journal a retro
// reads and on the board a human reads. Before this it landed on the same
// "merged" as an integration sub, next to a note saying the branch was pushed.
func TestExecuteEpicIndependentLandsOnPushed(t *testing.T) {
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope(workerVerified, "all green")+"'")
	store, _ := epicRunFixture(t)
	if _, err := store.MutateProject("app", func(p *storage.Project) error {
		p.Statuses = []string{"todo", "doing", "pushed", "waiting", "done"}
		p.Executor.DoneStatusIndependent = "pushed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	runEpic(t, store, epicOptions{independent: true})

	sub, _ := store.FindTask("app", "app-1-1")
	if sub.Meta.Status != storage.TaskStatus("pushed") {
		t.Errorf("a green batch sub must land on the independent status, got %q", sub.Meta.Status)
	}
	if strings.Contains(sub.Meta.Brief, "Worker merged") {
		t.Errorf("the sub's brief must not claim a merge: %q", sub.Meta.Brief)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	end := entries[len(entries)-1]
	if len(end.Subs) != 1 || end.Subs[0].Result != subPushed {
		t.Fatalf("journal must record the sub as pushed, got %+v", end.Subs)
	}
}

// The other side of the same coin: an integration sub really is merged, so it
// keeps saying so - the rename must not blur the two modes into one word again.
func TestExecuteEpicIntegrationStillRecordsMerged(t *testing.T) {
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope(workerVerified, "all green")+"'")
	store, _ := epicRunFixture(t)

	runEpic(t, store, epicOptions{noPR: true})

	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	end := entries[len(entries)-1]
	if len(end.Subs) != 1 || end.Subs[0].Result != subMerged {
		t.Fatalf("journal must record an integration sub as merged, got %+v", end.Subs)
	}
}

// A project whose statuses predate "pushed" must keep running batches: the
// built-in default degrades to done_status and says so on stderr.
func TestExecuteEpicIndependentDegradesWhenPushedIsNotAStatus(t *testing.T) {
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope(workerVerified, "all green")+"'")
	store, _ := epicRunFixture(t)

	_, stderr := runEpic(t, store, epicOptions{independent: true})

	sub, _ := store.FindTask("app", "app-1-1")
	if sub.Meta.Status != storage.StatusDone {
		t.Errorf("the run must still land the sub on done_status, got %q", sub.Meta.Status)
	}
	if !strings.Contains(stderr, "pushed") {
		t.Errorf("the degrade must be announced, not silent:\n%s", stderr)
	}
}

// epicRunFixtureN builds the epic fixture with n subs and pins the project's
// Claude config dir to a temp dir, so a fake worker can plant a transcript
// where pm will look for it without touching the real ~/.claude.
func epicRunFixtureN(t *testing.T, n int) (*storage.Store, string, string) {
	t.Helper()
	store, repo := epicRunFixture(t)
	cfg := t.TempDir()
	if _, err := store.MutateProject("app", func(p *storage.Project) error {
		p.ClaudeConfigDir = cfg
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= n; i++ {
		addTask(t, store, "app", storage.TaskMeta{
			ID: fmt.Sprintf("app-1-%d", i), Title: fmt.Sprintf("Sub %d", i),
			Status: storage.StatusTodo, Parent: "app-1", Order: 10 * i,
		}, "")
	}
	return store, repo, cfg
}

// TestExecuteEpicAbortsOnAnAccountWall is the whole point of pm-cli-89, end to
// end. In run pm-cli-74 the first worker died on the account's monthly spend
// limit and the manager, seeing only "exit status 1", spawned the next three
// workers into the same wall - they died 112 s, 2 s and 7 s later, each recorded
// as an ordinary failure with no cause anywhere in pm.
func TestExecuteEpicAbortsOnAnAccountWall(t *testing.T) {
	// The fake worker writes the limit message into the transcript pm pinned
	// with --session-id, then exits 1 - exactly what the four real deaths did.
	fakeClaude(t, `sid=""; prev=""
for a in "$@"; do
  if [ "$prev" = "--session-id" ]; then sid="$a"; fi
  prev="$a"
done
mkdir -p "$PM_TEST_TRANSCRIPT_DIR"
printf '%s\n' "{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"You have hit your monthly spend limit - raise it at claude.ai/settings/usage\"}]}}" > "$PM_TEST_TRANSCRIPT_DIR/$sid.jsonl"
exit 1`)
	// (The real message uses a typographic apostrophe in "You've"; the matched
	// substring starts at "hit your monthly spend limit", so the fake avoids a
	// quote the shell would have to escape three times over. The verbatim line
	// is asserted against accountWallReason in work_failure_test.go.)

	store, repo, cfg := epicRunFixtureN(t, 3)
	t.Setenv("PM_TEST_TRANSCRIPT_DIR", filepath.Join(cfg, "projects", encodeProjectPath(repo)))

	_, errOut := runEpic(t, store, epicOptions{noPR: true})

	if !strings.Contains(errOut, "ABORTING the run") {
		t.Fatalf("the run must stop and say why:\n%s", errOut)
	}
	if !strings.Contains(errOut, "spend limit") {
		t.Errorf("the reason must be named, not left as an exit status:\n%s", errOut)
	}

	// The sub that hit the wall never got a fair run: back on its ready status,
	// NOT parked on waiting (which a human would have to undo by hand).
	sub1, _ := store.FindTask("app", "app-1-1")
	if sub1.Meta.Status != storage.StatusTodo {
		t.Errorf("app-1-1 must return to todo, got %q", sub1.Meta.Status)
	}
	// The later subs were never started at all.
	for _, id := range []string{"app-1-2", "app-1-3"} {
		s, _ := store.FindTask("app", id)
		if s.Meta.Status != storage.StatusTodo {
			t.Errorf("%s must be untouched on todo, got %q", id, s.Meta.Status)
		}
	}

	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	var end *storage.JournalEntry
	for i := range entries {
		if entries[i].Event == storage.JournalEventEnd {
			end = &entries[i]
		}
	}
	if end == nil {
		t.Fatal("expected an end line in the journal")
	}
	if end.Status != storage.RunStatusFailed {
		t.Errorf("an aborted run must not be journalled as done, got %q", end.Status)
	}
	if !strings.Contains(end.Error, "spend limit") {
		t.Errorf("the run-level error must name the wall, got %q", end.Error)
	}
	// AC: distinguishable from an ordinary sub failure.
	if len(end.Subs) != 3 {
		t.Fatalf("every sub must be accounted for, got %d: %+v", len(end.Subs), end.Subs)
	}
	if end.Subs[0].Result != subAborted {
		t.Errorf("the sub that hit the wall must read %q, not a plain failure, got %q", subAborted, end.Subs[0].Result)
	}
	for _, s := range end.Subs[1:] {
		if s.Result != subSkipped {
			t.Errorf("%s never ran, so it must be %q, got %q", s.ID, subSkipped, s.Result)
		}
		if !strings.Contains(s.Note, "run aborted") {
			t.Errorf("%s's note must say why it did not run, got %q", s.ID, s.Note)
		}
	}
}

// A worker that dies for its OWN reason must still behave as before: one failed
// sub, parked, and the manager carries on to the next one.
func TestExecuteEpicContinuesAfterAnOrdinaryWorkerDeath(t *testing.T) {
	fakeClaude(t, `sid=""; prev=""
for a in "$@"; do
  if [ "$prev" = "--session-id" ]; then sid="$a"; fi
  prev="$a"
done
mkdir -p "$PM_TEST_TRANSCRIPT_DIR"
printf '%s\n' "{\"type\":\"assistant\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"I could not find the module the spec names.\"}]}}" > "$PM_TEST_TRANSCRIPT_DIR/$sid.jsonl"
exit 1`)

	store, repo, cfg := epicRunFixtureN(t, 2)
	t.Setenv("PM_TEST_TRANSCRIPT_DIR", filepath.Join(cfg, "projects", encodeProjectPath(repo)))

	_, errOut := runEpic(t, store, epicOptions{noPR: true})

	if strings.Contains(errOut, "ABORTING") {
		t.Fatalf("an ordinary worker death must not stop the run:\n%s", errOut)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	var end *storage.JournalEntry
	for i := range entries {
		if entries[i].Event == storage.JournalEventEnd {
			end = &entries[i]
		}
	}
	if end == nil || len(end.Subs) != 2 {
		t.Fatalf("both subs must have been attempted: %+v", end)
	}
	for _, s := range end.Subs {
		if s.Result != subFailed {
			t.Errorf("%s: want %q, got %q", s.ID, subFailed, s.Result)
		}
		// The headline of the other half: the note names a cause now.
		if !strings.Contains(s.Note, "could not find the module") {
			t.Errorf("%s's note must carry the worker's last message, got %q", s.ID, s.Note)
		}
	}
}

// TestExecuteEpicIndependentInASlotNeverTouchesTheLocalBase is the pm-cli-131
// regression at the level it was reported: a batch run in a worktree slot while
// the user's main checkout sits on `main`. Before the fix the run died with
// `checkout main for prepare: exit status 128` (git refuses a branch another
// worktree holds) and left the slot on `main`.
func TestExecuteEpicIndependentInASlotNeverTouchesTheLocalBase(t *testing.T) {
	fakeClaude(t, "git commit -q --allow-empty -m 'worker commit'\necho '"+envelope("merged", "all green")+"'")
	store, repo, slot := epicSlotFixture(t)

	baseBefore := gitReadT(t, repo, "rev-parse", "main")
	_, stderr := runEpic(t, store, epicOptions{independent: true, additional: true})

	if !strings.Contains(stderr, "off origin/main") {
		t.Errorf("the run should say the subs fork from origin/main:\n%s", stderr)
	}
	// The main checkout keeps its branch and its tip - pm never moves it.
	if got := gitReadT(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("main checkout left on %q, want main", got)
	}
	if got := gitReadT(t, repo, "rev-parse", "main"); got != baseBefore {
		t.Errorf("local main moved: %s -> %s", baseBefore, got)
	}
	// The run ends on the last sub's own branch (unchanged); what must never
	// happen is the slot holding the base branch - that is what wedged the
	// user's main checkout afterwards.
	if got := gitReadT(t, slot, "rev-parse", "--abbrev-ref", "HEAD"); got == "main" {
		t.Error("the slot ended holding the base branch - the main checkout can no longer switch to it")
	}
	if out, err := exec.Command("git", "-C", repo, "checkout", "main").CombinedOutput(); err != nil {
		t.Errorf("main checkout can no longer switch to main: %v\n%s", err, out)
	}
	sub, _ := store.FindTask("app", "app-1-1")
	if sub.Meta.Status != storage.StatusDone {
		t.Errorf("verify-green sub must reach the done status, got %q", sub.Meta.Status)
	}
	if log := gitLogAll(t, slot, "feat/only-sub"); !strings.Contains(log, "worker commit") {
		t.Errorf("the sub's branch should carry the worker's commit:\n%s", log)
	}
}

// epicSlotFixture is epicRunFixture with an origin and a worktree slot: the
// project is a clone whose main checkout stays on `main`, and the executor has
// one slot plus a prepare and a baseline (the two steps that used to check the
// base branch out).
func epicSlotFixture(t *testing.T) (*storage.Store, string, string) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitT(t, root, "init", "--bare", "-q", "--initial-branch=main", origin)
	repo := filepath.Join(root, "main")
	gitT(t, root, "clone", "-q", origin, repo)
	gitT(t, repo, "config", "user.email", "t@t")
	gitT(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")
	gitT(t, repo, "push", "-q", "-u", "origin", "main")

	slot := filepath.Join(root, "slot1")
	if err := store.CreateProject("app", &storage.Project{Name: "App", Path: repo, Executor: &storage.Executor{
		Enabled: true, StartStatus: "todo", DoneStatus: "done",
		Worktrees: []storage.WorktreeSlot{{Path: slot}},
		Prepare:   "true", Baseline: "true",
	}}); err != nil {
		t.Fatal(err)
	}
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1", Title: "Epic", Status: storage.StatusDoing}, "")
	addTask(t, store, "app", storage.TaskMeta{ID: "app-1-1", Title: "Only Sub", Status: storage.StatusTodo, Parent: "app-1", Order: 10}, "")
	return store, repo, slot
}
