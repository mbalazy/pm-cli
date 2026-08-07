package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// hangGuard bounds every wait in this file. It only has to beat the 300s sleeps
// the fixtures hold open, so it is generous on purpose: these cases run
// alongside hundreds of git forks in this package, and a budget tuned to the
// happy path would turn `make check` red under load rather than on a bug.
const hangGuard = 60 * time.Second

// procFixture is the shared harness for the hang paths: it runs the subject off
// the test goroutine (a regression fails the guard instead of wedging the whole
// suite) and guarantees that neither a leaked `sleep 300` nor a still-running
// subject outlives the case - including when the case FAILS.
type procFixture struct {
	t   *testing.T
	dir string
	// pid files the fake writes; recorded up front so a case that never reaches
	// awaitPid still cleans up whatever did get spawned.
	files []string
	done  chan struct{}
}

// newProcFixture shrinks the tuning knobs and registers cleanup. Cleanup order
// is LIFO and load-bearing: kill the spawned tree -> join the subject goroutine
// (the kill is what unblocks it) -> only then restore the knobs, so a failing
// case cannot race the restore.
func newProcFixture(t *testing.T, waitDelay time.Duration) *procFixture {
	t.Helper()
	oldDelay, oldBaseline, oldGrace := procWaitDelay, baselineTimeout, procKillGrace
	procWaitDelay = waitDelay
	t.Cleanup(func() {
		procWaitDelay, baselineTimeout, procKillGrace = oldDelay, oldBaseline, oldGrace
	})

	f := &procFixture{t: t, dir: t.TempDir(), done: make(chan struct{})}
	t.Cleanup(f.killSpawned)
	t.Cleanup(func() {
		select {
		case <-f.done:
		case <-time.After(hangGuard):
			t.Error("subject never returned - leaked goroutine")
		}
	})
	return f
}

// pidFile reserves a path the fake shell script writes a pid to.
func (f *procFixture) pidFile(name string) string {
	p := filepath.Join(f.dir, name+".pid")
	f.files = append(f.files, p)
	return p
}

func (f *procFixture) killSpawned() {
	for _, p := range f.files {
		if pid, err := readPidFile(p); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// run starts the subject on its own goroutine; wait blocks until it returns.
func (f *procFixture) run(subject func()) {
	go func() {
		defer close(f.done)
		subject()
	}()
}

func (f *procFixture) wait(what string) {
	f.t.Helper()
	select {
	case <-f.done:
	case <-time.After(hangGuard):
		f.t.Fatalf("%s hung past its deadline - something held the output pipe and blocked Wait", what)
	}
}

// awaitPid waits for the fake to register a process. Deliberately does NOT
// assert the process is still alive: production is racing to kill it, and that
// race is the point of the test, not an invariant.
func (f *procFixture) awaitPid(path string) int {
	f.t.Helper()
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if pid, err := readPidFile(path); err == nil {
			return pid
		}
		select {
		case <-f.done:
			f.t.Fatalf("subject finished before the fake registered a pid in %s", path)
		case <-time.After(20 * time.Millisecond):
		}
	}
	f.t.Fatalf("nothing ever recorded a pid in %s", path)
	return 0
}

func readPidFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func (f *procFixture) requireGone(pid int, what string) {
	f.t.Helper()
	requirePidGone(f.t, pid, what)
}

func requirePidGone(t *testing.T, pid int, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s (pid %d) survived the run - the process group was not killed", what, pid)
}

// spawnGrandchild starts a DETACHED grandchild that records its own pid and
// holds the stdout it inherited open for minutes. This is the shape that used
// to wedge pm forever: killing the direct child never reached it, and Wait
// blocked on the pipe it kept open.
func spawnGrandchild(pidFile string) string {
	return "sh -c 'echo $$ > " + pidFile + "; exec sleep 300' &\n"
}

// holdOpen makes the fake itself linger (recording the pid of its own child) so
// a test can assert the DIRECT child dies too, not just the detached one.
func holdOpen(pidFile string) string {
	return "sleep 300 & echo $! > " + pidFile + "\nwait\n"
}

func TestRunWorkerTimeoutKillsWholeWorkerTree(t *testing.T) {
	f := newProcFixture(t, 500*time.Millisecond)
	grandchild, child := f.pidFile("grandchild"), f.pidFile("child")
	// The worker itself also hangs, so the deadline - not a clean exit - is what
	// has to take the tree down.
	fakeClaude(t, spawnGrandchild(grandchild)+holdOpen(child))

	var res *workerResult
	var err error
	f.run(func() {
		res, _, err = runWorker(os.Stderr, t.TempDir(), []string{"-p", "x"}, 1500*time.Millisecond, "", nil, "", nil)
	})

	gcPid, cPid := f.awaitPid(grandchild), f.awaitPid(child)
	f.wait("runWorker")

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want the timeout error, got res=%+v err=%v", res, err)
	}
	if res != nil {
		t.Fatalf("timed-out worker must not yield a result: %+v", res)
	}
	f.requireGone(cPid, "the worker's direct child")
	f.requireGone(gcPid, "the worker's detached grandchild")
}

func TestRunWorkerTimeoutKillsWorkerIgnoringSIGTERM(t *testing.T) {
	f := newProcFixture(t, 500*time.Millisecond)
	procKillGrace = 300 * time.Millisecond
	grandchild := f.pidFile("grandchild")
	// A worker tree that swallows SIGTERM - the escalation to SIGKILL is the
	// only thing that can end it.
	fakeClaude(t, "trap '' TERM\n"+spawnGrandchild(grandchild)+"trap '' TERM; sleep 300\n")

	var err error
	f.run(func() {
		_, _, err = runWorker(os.Stderr, t.TempDir(), []string{"-p", "x"}, 1500*time.Millisecond, "", nil, "", nil)
	})

	pid := f.awaitPid(grandchild)
	f.wait("runWorker")

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want the timeout error, got %v", err)
	}
	f.requireGone(pid, "the SIGTERM-ignoring worker tree")
}

func TestRunWorkerOrphanHoldingStdoutDoesNotHang(t *testing.T) {
	f := newProcFixture(t, 2*time.Second)
	grandchild := f.pidFile("grandchild")
	// Worker finishes cleanly but leaves a background process on the inherited
	// stdout: Wait must be bounded by WaitDelay rather than by that process, and
	// the completed run's envelope must survive.
	fakeClaude(t, spawnGrandchild(grandchild)+"echo '"+envelope(workerVerified, "done")+"'\n")

	var res *workerResult
	var err error
	f.run(func() {
		res, _, err = runWorker(os.Stderr, t.TempDir(), []string{"-p", "x"}, time.Minute, "", nil, "", nil)
	})

	pid := f.awaitPid(grandchild)
	f.wait("runWorker")

	if err != nil {
		t.Fatalf("a finished worker's result must survive its background leftovers: %v", err)
	}
	if res == nil || res.Status != workerVerified {
		t.Fatalf("result not parsed: %+v", res)
	}
	f.requireGone(pid, "the orphan holding stdout")
}

func TestCaptureBaselineCapKillsWholeTree(t *testing.T) {
	f := newProcFixture(t, 500*time.Millisecond)
	baselineTimeout = 1500 * time.Millisecond
	grandchild, child := f.pidFile("grandchild"), f.pidFile("child")

	var got string
	f.run(func() { got = captureBaseline(os.Stderr, f.dir, spawnGrandchild(grandchild)+holdOpen(child)) })

	gcPid, cPid := f.awaitPid(grandchild), f.awaitPid(child)
	f.wait("captureBaseline")

	if got != "" {
		t.Fatalf("a baseline that blew its cap must degrade to no baseline, got %q", got)
	}
	f.requireGone(cPid, "the baseline's direct child")
	f.requireGone(gcPid, "the baseline's detached grandchild")
}

func TestCaptureBaselineGreenSurvivesOrphanHoldingOutput(t *testing.T) {
	f := newProcFixture(t, 2*time.Second)
	grandchild := f.pidFile("grandchild")

	// Wait reports ErrWaitDelay ONLY when the command itself exited 0, so a
	// baseline whose leftovers held the pipe open is still green - degrading to
	// "no baseline" would cost every worker in the run its pre-existing-failure
	// reference.
	var got string
	f.run(func() { got = captureBaseline(os.Stderr, f.dir, spawnGrandchild(grandchild)+"echo all-good\nexit 0\n") })

	pid := f.awaitPid(grandchild)
	f.wait("captureBaseline")

	mustContain(t, got, "GREEN")
	f.requireGone(pid, "the orphan holding the baseline output")
}

// forwardChildEnv puts the re-exec'd test binary into "manager" mode.
const forwardChildEnv = "PM_TEST_FORWARD_CHILD"

// TestForwardedSignalTakesTheWorkerTreeDown covers the half of the design that
// no in-process test can reach: because the worker leads its OWN group, the
// board's killRun (which signals the MANAGER's group) no longer reaches it, and
// the manager forwarding the signal onwards is the only thing that still takes
// the worker tree down. A manager that just died would leave a headless worker
// running for the rest of its timeout in a worktree the board has already
// unlocked - so this re-execs the test binary as a stand-in manager, SIGTERMs
// it the way killRun does, and checks the worker's descendants died with it.
func TestForwardedSignalTakesTheWorkerTreeDown(t *testing.T) {
	if dir := os.Getenv(forwardChildEnv); dir != "" {
		// Manager role: block on a worker that would otherwise run for 10m.
		_, _, _ = runWorker(os.Stderr, dir, []string{"-p", "x"}, 10*time.Minute, "", nil, "", nil)
		return
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	childFile := filepath.Join(dir, "child.pid")
	// Both pids are recorded so cleanup can reap the tree even when the case
	// FAILS - which is exactly when the worker survives.
	script := "#!/bin/sh\n" + spawnGrandchild(pidFile) + holdOpen(childFile)
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=5m")
	env := []string{forwardChildEnv + "=" + dir, "PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PATH=") || strings.HasPrefix(kv, forwardChildEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	child.Env = env
	if err := child.Start(); err != nil {
		t.Fatalf("re-exec: %v", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	t.Cleanup(func() {
		_ = child.Process.Kill()
		for _, f := range []string{pidFile, childFile} {
			if pid, err := readPidFile(f); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	// Wait for the manager's worker tree to be up, then stop the manager.
	var pid int
	deadline := time.Now().Add(hangGuard)
	for pid == 0 && time.Now().Before(deadline) {
		if p, err := readPidFile(pidFile); err == nil {
			pid = p
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the re-exec'd manager never launched a worker")
	}
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal manager: %v", err)
	}

	select {
	case <-waited: // the manager must still die on a SIGTERM it forwards
	case <-time.After(hangGuard):
		t.Fatal("the manager survived the SIGTERM it was supposed to forward and re-raise")
	}
	requirePidGone(t, pid, "the worker tree of a SIGTERM'd manager")
}

// TestExecuteWorkPublishesTheWorkerGroup covers the handle an outside observer
// needs: Setpgid moved the worker out of the manager's group, so the board can
// no longer reach it by signalling the manager - and the manager's own
// forwarding dies with a SIGKILL. The pgid on the run-state is what is left, so
// it has to be published while the worker runs, name the group the worker's
// DESCENDANTS are in, and be gone once the worker is.
func TestExecuteWorkPublishesTheWorkerGroup(t *testing.T) {
	f := newProcFixture(t, 500*time.Millisecond)
	grandchild := f.pidFile("grandchild")
	fakeClaude(t, spawnGrandchild(grandchild)+"sleep 300\n")
	store, task, plan, opts := executorFixture(t)
	opts.timeout = 3 * time.Second

	f.run(func() { _, _ = executeWork(store, task, plan, opts) })

	gcPid := f.awaitPid(grandchild)
	stateDir := store.ProjectDir("app")
	var pgid int
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) && pgid == 0 {
		if run, err := storage.ReadRunState(stateDir, "app-1"); err == nil {
			pgid = run.WorkerPGID
		}
		if pgid == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if pgid == 0 {
		t.Fatal("the worker's pgid was never published to the run-state")
	}
	if pgid == os.Getpid() {
		t.Fatal("the manager published its OWN pid - signalling that group is a self-kill")
	}
	// The published pgid is only useful if the worker's descendants are actually
	// in it: that is what makes a kill(-pgid) reach the tree.
	gcGroup, err := syscall.Getpgid(gcPid)
	if err != nil {
		t.Fatalf("getpgid(%d): %v", gcPid, err)
	}
	if gcGroup != pgid {
		t.Fatalf("published pgid %d is not the worker tree's group %d", pgid, gcGroup)
	}

	f.wait("executeWork")
	f.requireGone(gcPid, "the worker's grandchild")
	run, rerr := storage.ReadRunState(stateDir, "app-1")
	if rerr != nil {
		t.Fatalf("run-state: %v", rerr)
	}
	// A pgid left behind is a pid the board would signal on the next kill, by
	// which time the kernel may have handed it to something unrelated.
	if run.WorkerPGID != 0 {
		t.Fatalf("stale worker pgid %d left on the run-state after the worker exited", run.WorkerPGID)
	}
}

func TestExecuteWorkRecordsTimeoutOutcome(t *testing.T) {
	f := newProcFixture(t, 500*time.Millisecond)
	grandchild := f.pidFile("grandchild")
	fakeClaude(t, spawnGrandchild(grandchild)+"sleep 300\n")
	store, task, plan, opts := executorFixture(t)
	opts.timeout = 1500 * time.Millisecond

	var err error
	f.run(func() { _, err = executeWork(store, task, plan, opts) })

	pid := f.awaitPid(grandchild)
	f.wait("executeWork")

	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("executeWork must surface the timeout, got %v", err)
	}
	f.requireGone(pid, "the worker's grandchild")

	run, rerr := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if rerr != nil {
		t.Fatalf("run-state: %v", rerr)
	}
	if run.Status != storage.RunStatusFailed || !strings.Contains(run.Error, "timed out") {
		t.Fatalf("timeout outcome not recorded on the run-state: %+v", run)
	}
	if len(run.Subs) != 1 || run.Subs[0].Status != storage.RunStatusFailed ||
		!strings.Contains(run.Subs[0].Note, "timed out") {
		t.Fatalf("in-flight sub not marked failed: %+v", run.Subs)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || entries[1].Event != "end" || entries[1].Status != storage.RunStatusFailed ||
		!strings.Contains(entries[1].Error, "timed out") {
		t.Fatalf("timeout outcome not journaled: %+v", entries)
	}
	if len(entries[1].Subs) != 1 || entries[1].Subs[0].Result != "failed" {
		t.Fatalf("journal sub outcome not recorded: %+v", entries[1].Subs)
	}
}
