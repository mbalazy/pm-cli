package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// spawnGrandchild is a shell snippet that starts a DETACHED grandchild which
// records its own pid in pidFile and then holds the stdout it inherited open
// for minutes. This is the shape that used to wedge pm forever: killing the
// direct child never reached it, and Wait blocked on the pipe it kept open.
func spawnGrandchild(pidFile string) string {
	return "sh -c 'echo $$ > " + pidFile + "; exec sleep 300' &\n"
}

// shrinkProcWaitDelay keeps the pipe-holder tests to seconds instead of the
// production 5s per case.
func shrinkProcWaitDelay(t *testing.T, d time.Duration) {
	t.Helper()
	old := procWaitDelay
	procWaitDelay = d
	t.Cleanup(func() { procWaitDelay = old })
}

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// awaitGrandchild waits for the fake's grandchild to register itself, asserts it
// is really running, and makes sure it cannot outlive a failing test.
func awaitGrandchild(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
				if !pidAlive(pid) {
					t.Fatalf("fixture broken: grandchild %d already gone", pid)
				}
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild never recorded its pid in %s", pidFile)
	return 0
}

func requirePidGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived the run - the process group was not killed", pid)
}

func TestRunWorkerTimeoutKillsWholeWorkerTree(t *testing.T) {
	shrinkProcWaitDelay(t, 500*time.Millisecond)
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// The worker itself also hangs, so the deadline - not a clean exit - is what
	// has to take the tree down.
	fakeClaude(t, spawnGrandchild(pidFile)+"sleep 300\n")

	type outcome struct {
		res *workerResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, _, err := runWorker(t.TempDir(), []string{"-p", "x"}, 1500*time.Millisecond, "", nil)
		done <- outcome{res, err}
	}()

	pid := awaitGrandchild(t, pidFile)

	select {
	case got := <-done:
		if got.err == nil || !strings.Contains(got.err.Error(), "timed out") {
			t.Fatalf("want the timeout error, got res=%+v err=%v", got.res, got.err)
		}
		if got.res != nil {
			t.Fatalf("timed-out worker must not yield a result: %+v", got.res)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("runWorker hung far past its 1.5s timeout - a descendant holding the stdout pipe blocked Wait")
	}
	requirePidGone(t, pid, 5*time.Second)
}

func TestRunWorkerOrphanHoldingStdoutDoesNotHang(t *testing.T) {
	shrinkProcWaitDelay(t, 500*time.Millisecond)
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// Worker finishes cleanly, but leaves a background process on the inherited
	// stdout: Wait must be bounded by WaitDelay rather than by that process, and
	// the completed run's envelope must survive.
	fakeClaude(t, spawnGrandchild(pidFile)+"echo '"+envelope("merged", "done")+"'\n")

	type outcome struct {
		res *workerResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		// A generous timeout: only the WaitDelay bound can end this run.
		res, _, err := runWorker(t.TempDir(), []string{"-p", "x"}, time.Minute, "", nil)
		done <- outcome{res, err}
	}()

	pid := awaitGrandchild(t, pidFile)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("a finished worker's result must survive its background leftovers: %v", got.err)
		}
		if got.res == nil || got.res.Status != "merged" {
			t.Fatalf("result not parsed: %+v", got.res)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runWorker hung after the worker exited - a descendant still held the stdout pipe")
	}
	requirePidGone(t, pid, 5*time.Second)
}

func TestCaptureBaselineCapKillsWholeTree(t *testing.T) {
	shrinkProcWaitDelay(t, 500*time.Millisecond)
	old := baselineTimeout
	baselineTimeout = 1500 * time.Millisecond
	t.Cleanup(func() { baselineTimeout = old })

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")

	done := make(chan string, 1)
	go func() { done <- captureBaseline(dir, spawnGrandchild(pidFile)+"sleep 300\n") }()

	pid := awaitGrandchild(t, pidFile)

	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("a baseline that blew its cap must degrade to no baseline, got %q", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("captureBaseline hung far past its cap - a descendant holding the output pipe blocked Wait")
	}
	requirePidGone(t, pid, 5*time.Second)
}

func TestExecuteWorkRecordsTimeoutOutcome(t *testing.T) {
	shrinkProcWaitDelay(t, 500*time.Millisecond)
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	fakeClaude(t, spawnGrandchild(pidFile)+"sleep 300\n")
	store, task, plan, opts := executorFixture(t)
	opts.timeout = 1500 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, err := executeWork(store, task, plan, opts)
		done <- err
	}()

	pid := awaitGrandchild(t, pidFile)

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("executeWork must surface the timeout, got %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("executeWork hung past the worker timeout")
	}
	requirePidGone(t, pid, 5*time.Second)

	run, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil {
		t.Fatalf("run-state: %v", err)
	}
	if run.Status != storage.RunStatusFailed || !strings.Contains(run.Error, "timed out") {
		t.Fatalf("timeout outcome not recorded on the run-state: %+v", run)
	}
	if len(run.Subs) != 1 || run.Subs[0].Status != storage.RunStatusFailed {
		t.Fatalf("in-flight sub not marked failed: %+v", run.Subs)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || entries[1].Event != "end" || entries[1].Status != storage.RunStatusFailed ||
		!strings.Contains(entries[1].Error, "timed out") {
		t.Fatalf("timeout outcome not journaled: %+v", entries)
	}
}
