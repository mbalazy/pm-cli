package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// The acceptance draws its worktree slot from the SAME pool `pm work` and
// `pm run-epic` draw on - that shared pool is why the acceptance became a pm run at
// all (it needs the simulator and the dev-server port the batch may still be
// holding). These tests cover the claim, the refusals, and the two things the
// acceptance deliberately does NOT do to a slot.
//
// pm-cli itself configures no slots, so everything here stands up a synthetic
// project under its own store root - the pattern work_worktree_test.go uses.

// finishSlotFixture: a store with one project whose executor has n worktree
// slots (each with its own env), a tracker, and the project's repo path.
func finishSlotFixture(t *testing.T, n int) (*storage.Store, *storage.Task, []string) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	repo := t.TempDir()
	gitInitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	slotRoot := t.TempDir()
	var slots []storage.WorktreeSlot
	var paths []string
	for i := 1; i <= n; i++ {
		p := filepath.Join(slotRoot, "slot"+string(rune('0'+i)))
		paths = append(paths, p)
		slots = append(slots, storage.WorktreeSlot{
			Path: p,
			Env:  map[string]string{"SLOT_PORT": "80" + string(rune('0'+i)) + "0"},
		})
	}
	if err := store.CreateProject("app", &storage.Project{
		Name: "App", Path: repo,
		Executor: &storage.Executor{Enabled: true, Worktrees: slots},
	}); err != nil {
		t.Fatal(err)
	}
	addTask(t, store, "app", storage.TaskMeta{ID: "app-9", Title: "Batch tracker", Status: storage.StatusDoing}, "")
	tracker, err := store.FindTask("app", "app-9")
	if err != nil {
		t.Fatal(err)
	}
	return store, tracker, paths
}

// liveHolderPID is a live pid that is NOT ours - the idiom work_worktree_test.go
// already uses for the same reason: LiveWorktreeHolder reads a lock held by
// os.Getpid() as free (the epic manager re-enters its own slot per sub), so a
// test that locked with its own pid would find every slot free and prove
// nothing. The test runner's parent is alive and older than any lock we write.
func liveHolderPID(t *testing.T) int {
	t.Helper()
	return os.Getppid()
}

func finishSlotOpts(t *testing.T, additional bool, pin int) finishOptions {
	t.Helper()
	return finishOptions{
		model: "opus", maxTurns: 10, yolo: true, timeout: 30 * time.Second,
		additional: additional, slotPin: pin, errOut: &strings.Builder{},
	}
}

// The first free slot is claimed for the whole run, its path becomes the
// worker's cwd AND the run-state's RepoPath (which is how the board resolves
// the transcript), its env reaches the worker, and the lock is freed on the way
// out.
func TestFinishAdditionalClaimsAFreeSlot(t *testing.T) {
	store, tracker, paths := finishSlotFixture(t, 2)
	dir := store.ProjectDir("app")

	// The fake worker records where it ran and what env it was handed, then
	// emits an ordinary acceptance envelope.
	spy := filepath.Join(t.TempDir(), "spy")
	fakeClaude(t, "pwd > "+spy+"; echo \"$SLOT_PORT\" >> "+spy+"; cat <<'EOF'\n"+
		finishEnvelopeJSON(t, finishDone, "accepted", "# Report\n")+"\nEOF\n")

	plan, err := planFinish(store, tracker, "app", finishSlotOpts(t, true, 0))
	if err != nil {
		t.Fatalf("planFinish: %v", err)
	}
	if !plan.worktree || len(plan.slots) != 2 {
		t.Fatalf("plan did not resolve the pool: worktree=%v slots=%+v", plan.worktree, plan.slots)
	}
	if _, err := executeFinish(plan); err != nil {
		t.Fatalf("executeFinish: %v", err)
	}

	got, err := os.ReadFile(spy)
	if err != nil {
		t.Fatalf("the fake worker recorded nothing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 2 {
		t.Fatalf("spy = %q, want cwd + SLOT_PORT", got)
	}
	if resolveSymlinks(t, lines[0]) != resolveSymlinks(t, paths[0]) {
		t.Errorf("worker cwd = %s, want the claimed slot %s", lines[0], paths[0])
	}
	if lines[1] != "8010" {
		t.Errorf("SLOT_PORT = %q, want slot 1's env to reach the worker", lines[1])
	}
	// The prompt must name the SLOT, not the provisional path it was planned
	// against - a worker told the wrong absolute path can walk out of its
	// isolation by following it.
	if !strings.Contains(plan.prompt, paths[0]) {
		t.Errorf("prompt was not re-rendered for the claimed slot:\n%s", firstLines(plan.prompt, 12))
	}

	run, err := storage.ReadFinishRunState(dir, "app-9")
	if err != nil {
		t.Fatalf("acceptance run-state: %v", err)
	}
	if run.RepoPath != paths[0] {
		t.Errorf("RunState.RepoPath = %s, want the claimed slot %s (the board resolves the transcript from it)", run.RepoPath, paths[0])
	}
	if lk, _ := storage.ReadWorktreeLock(paths[0]); lk != nil {
		t.Errorf("the slot lock must be released when the run ends: %+v", lk)
	}
}

// Every slot busy: the error names each holder, so the user can pick what to
// wait for or kill - and nothing is claimed, written or spawned.
func TestFinishAdditionalAllSlotsBusyListsHolders(t *testing.T) {
	store, tracker, paths := finishSlotFixture(t, 2)
	fakeClaude(t, "echo 'the worker must never be reached'; exit 1")

	proj, _ := store.GetProject("app")
	for i, p := range paths {
		if err := storage.EnsureWorktree(proj.Path, p); err != nil {
			t.Fatal(err)
		}
		if err := storage.AcquireWorktreeLock(p, "app-"+string(rune('1'+i)), "work", liveHolderPID(t)); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := planFinish(store, tracker, "app", finishSlotOpts(t, true, 0))
	if err != nil {
		t.Fatalf("planFinish: %v", err)
	}
	_, err = executeFinish(plan)
	if err == nil {
		t.Fatal("a run with every slot busy must fail, not silently fall back to the main checkout")
	}
	for _, want := range []string{"all 2 worktree slot(s) busy", paths[0], paths[1], "app-1", "app-2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name every holder, missing %q:\n%v", want, err)
		}
	}
	// A refused run leaves nothing behind - in particular not the acceptance
	// claim, which would lock the tracker out for the rest of the TTL.
	dir := store.ProjectDir("app")
	if claim, _ := storage.ReadFinishClaim(dir, "app-9"); claim != nil {
		t.Errorf("the acceptance claim must be released when the slot claim fails: %+v", claim)
	}
	if _, err := storage.ReadFinishRunState(dir, "app-9"); err == nil {
		t.Error("no run-state should be written for a run that never started")
	}
}

// --slot N pins one slot and fails fast when it is busy, rather than quietly
// taking a different one.
func TestFinishSlotPinFailsFastWhenBusy(t *testing.T) {
	store, tracker, paths := finishSlotFixture(t, 2)
	proj, _ := store.GetProject("app")
	fakeClaude(t, "echo 'the worker must never be reached'; exit 1")
	if err := storage.EnsureWorktree(proj.Path, paths[1]); err != nil {
		t.Fatal(err)
	}
	if err := storage.AcquireWorktreeLock(paths[1], "app-1", "work", liveHolderPID(t)); err != nil {
		t.Fatal(err)
	}

	plan, err := planFinish(store, tracker, "app", finishSlotOpts(t, true, 2))
	if err != nil {
		t.Fatalf("planFinish: %v", err)
	}
	if _, err := executeFinish(plan); err == nil || !strings.Contains(err.Error(), "slot 2") {
		t.Fatalf("--slot 2 on a busy slot must fail naming it, got: %v", err)
	}
	// Slot 1 was free the whole time and must NOT have been taken: a pin is a
	// pin.
	if lk, _ := storage.ReadWorktreeLock(paths[0]); lk != nil {
		t.Errorf("--slot 2 fell back to slot 1: %+v", lk)
	}
}

// --additional on a project with no slots configured is the one shared error
// message (errNoWorktreeSlots), not a silent run in the main checkout.
func TestFinishAdditionalWithoutSlotsIsRefused(t *testing.T) {
	store, _, _ := finishRunFixture(t)
	tracker, err := store.FindTask("app", "app-9")
	if err != nil {
		t.Fatal(err)
	}
	_, err = planFinish(store, tracker, "app", finishSlotOpts(t, true, 0))
	if err == nil || !strings.Contains(err.Error(), "no worktree slots configured") {
		t.Fatalf("--additional without slots must be refused, got: %v", err)
	}
}

// Without --additional nothing changes: the acceptance stands in the main
// checkout, claims no slot, and does not require a clean tree.
func TestFinishWithoutAdditionalStaysInTheMainCheckout(t *testing.T) {
	store, tracker, paths := finishSlotFixture(t, 2)
	proj, _ := store.GetProject("app")
	// A dirty tree the acceptance must NOT refuse: it forks no branch, so
	// nothing of the user's would be switched out from under them.
	if err := os.WriteFile(filepath.Join(proj.Path, "dirty.txt"), []byte("uncommitted\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fakeClaudeEnvelope(t, finishEnvelopeJSON(t, finishDone, "accepted", "# Report\n"))

	plan, err := planFinish(store, tracker, "app", finishSlotOpts(t, false, 0))
	if err != nil {
		t.Fatalf("planFinish: %v", err)
	}
	if plan.worktree || plan.workDir != proj.Path || len(plan.slots) != 0 {
		t.Fatalf("a run without --additional must target the main checkout: worktree=%v dir=%s slots=%+v",
			plan.worktree, plan.workDir, plan.slots)
	}
	if _, err := executeFinish(plan); err != nil {
		t.Fatalf("executeFinish on a dirty main checkout: %v", err)
	}
	for _, p := range paths {
		if lk, _ := storage.ReadWorktreeLock(p); lk != nil {
			t.Errorf("a run without --additional claimed slot %s: %+v", p, lk)
		}
	}
}

// The acceptance NEVER wipes or re-branches its slot, unlike `pm work`: it
// forks no branch of its own, and `batch-finish-auto` puts each sub's branch in
// a worktree it creates itself. A wipe would destroy exactly that work.
func TestFinishDoesNotWipeOrRebranchTheSlot(t *testing.T) {
	store, tracker, paths := finishSlotFixture(t, 1)
	proj, _ := store.GetProject("app")
	if err := storage.EnsureWorktree(proj.Path, paths[0]); err != nil {
		t.Fatal(err)
	}
	gitT(t, paths[0], "checkout", "-q", "-b", "some/sub-branch")
	leftover := filepath.Join(paths[0], "in-progress.txt")
	if err := os.WriteFile(leftover, []byte("acceptance work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths[0], "f.txt"), []byte("edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fakeClaudeEnvelope(t, finishEnvelopeJSON(t, finishDone, "accepted", "# Report\n"))

	plan, err := planFinish(store, tracker, "app", finishSlotOpts(t, true, 0))
	if err != nil {
		t.Fatalf("planFinish: %v", err)
	}
	if _, err := executeFinish(plan); err != nil {
		t.Fatalf("executeFinish: %v", err)
	}

	if _, err := os.Stat(leftover); err != nil {
		t.Errorf("the acceptance wiped an untracked file in its slot: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(paths[0], "f.txt")); strings.TrimSpace(string(got)) != "edited" {
		t.Errorf("the acceptance reset tracked changes in its slot: %q", got)
	}
	if br := strings.TrimSpace(gitOut(paths[0], "rev-parse", "--abbrev-ref", "HEAD")); br != "some/sub-branch" {
		t.Errorf("the acceptance re-branched its slot: HEAD = %q, want the branch it found", br)
	}
}

// The dry-run prints the pool it WOULD claim from - and says out loud that the
// slot is not wiped, which is where --additional means something different here
// than it does for `pm work`.
func TestFinishDryRunPrintsTheSlotPool(t *testing.T) {
	store, _, paths := finishSlotFixture(t, 2)
	out, err := runFinishCmd(t, store, "app-9", "--project", "app", "--additional", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{"ADDITIONAL worktree", "first free of 2 slot(s)", paths[0], paths[1], "SLOT_PORT=8010", "never wiped or re-branched"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run must print %q:\n%s", want, out)
		}
	}
	// Nothing was claimed - a dry-run must not take a slot away from a real run.
	for _, p := range paths {
		if lk, _ := storage.ReadWorktreeLock(p); lk != nil {
			t.Errorf("the dry-run claimed slot %s: %+v", p, lk)
		}
	}

	plain, err := runFinishCmd(t, store, "app-9", "--project", "app", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, plain)
	}
	if !strings.Contains(plain, "run: DEFAULT (main checkout") {
		t.Errorf("a run without --additional must say so:\n%s", plain)
	}
}

// A --slot the run would refuse must be refused by the dry-run too: a dry-run
// that reports readiness it never checked is the check people run instead of
// the real thing.
func TestFinishDryRunRefusesAnOutOfRangeSlotPin(t *testing.T) {
	store, tracker, _ := finishSlotFixture(t, 2)
	_, err := planFinish(store, tracker, "app", finishSlotOpts(t, true, 9))
	if err == nil || !strings.Contains(err.Error(), "--slot 9 out of range") {
		t.Fatalf("an out-of-range pin must be refused while planning, got: %v", err)
	}
	out, cerr := runFinishCmd(t, store, "app-9", "--project", "app", "--additional", "--slot", "9", "--dry-run")
	if cerr == nil {
		t.Fatalf("the dry-run must exit non-zero on a pin the run would refuse:\n%s", out)
	}
}

// resolveSymlinks makes a path comparison survive macOS's /var -> /private/var:
// the worker reports its cwd via `pwd`, the config carries the configured path.
func resolveSymlinks(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}
