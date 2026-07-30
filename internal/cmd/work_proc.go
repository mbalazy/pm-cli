package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

// procWaitDelay bounds exec.Cmd.Wait on the two paths that would otherwise
// block pm forever. Both are real for a `claude -p` worker: output is collected
// through an os.Pipe that EVERY descendant inherits, so Wait blocks until the
// last holder of the write end closes it - long after the child itself is gone,
// and long past the deadline. A var, not a const, purely so tests can shrink it
// (same trick as workerHeartbeatInterval).
var procWaitDelay = 5 * time.Second

// procKillGrace is how long a signalled worker group gets to go down on its own
// before pm escalates to SIGKILL. Kept well under the board's own 2s escalation
// window (killRun -> execKillCheckMsg): when killRun SIGTERMs the manager, the
// manager must have finished taking its worker tree down - and died itself -
// before the board re-checks.
var procKillGrace = time.Second

// groupCmd builds an exec.Cmd that runs in its OWN process group, so the whole
// worker tree - not just the direct child - can be signalled. Without it,
// exec.CommandContext's cancellation reaches only the process pm spawned: a
// timed-out worker's test runner / dev server / nested agent survives, keeps
// the inherited stdout pipe open, and the deadline enforces nothing.
//
// The flip side is that the worker no longer shares pm's group, so nothing
// OUTSIDE this process can reach it any more (the board's killRun signals the
// MANAGER's group). That is why every exit path here - deadline, WaitDelay, a
// forwarded terminal signal - takes the group down explicitly before pm leaves.
func groupCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Default cancellation is Process.Kill() - the direct child only. Signal the
	// group instead, SIGTERM first so a worker gets to flush, SIGKILL behind it.
	c.Cancel = func() error {
		stopGroup(c.Process.Pid)
		return nil // let Wait report the process's own error, not this one
	}
	c.WaitDelay = procWaitDelay
	return c
}

// runGroupCmd starts c (built by groupCmd), waits for it, and makes sure the
// group leaves nothing behind. Wait's error is returned verbatim - callers
// distinguish a plain non-zero exit (*exec.ExitError) from exec.ErrWaitDelay,
// which means "the process exited 0 but a descendant still held its output".
func runGroupCmd(ctx context.Context, c *exec.Cmd) error {
	// Armed BEFORE the fork: between Start and Notify there would otherwise be a
	// window where a terminal signal kills pm with the default disposition and
	// leaves a brand-new worker - already in its own group - unreachable. The
	// pid is published through an atomic because the forwarder runs on another
	// goroutine; until it is set there is nothing to forward to.
	var pid atomic.Int64
	stopForwarding := forwardTerminalSignals(&pid)
	defer stopForwarding()

	if err := c.Start(); err != nil {
		return err
	}
	pid.Store(int64(c.Process.Pid))

	err := c.Wait()
	// Only the abnormal paths can leave descendants that are ours to collect:
	// the deadline fired (Cancel signalled the group - confirm nothing survived
	// the grace), or WaitDelay had to unblock us because something still held
	// the output pipe. A plain non-zero exit is NOT one of them: a red
	// executor.baseline is the routine case, and blanket-signalling a group
	// whose leader Wait has just reaped would risk a recycled pid for nothing.
	if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) {
		_ = signalGroup(c.Process.Pid, syscall.SIGKILL)
	}
	return err
}

// stopGroup takes the process group led by pid down: SIGTERM, a short grace
// period to let it exit on its own, then SIGKILL for whatever is still there.
func stopGroup(pid int) {
	if pid <= 0 {
		return // never fall through to kill(0) / kill(-0) - that is OUR group
	}
	_ = signalGroup(pid, syscall.SIGTERM)
	deadline := time.Now().Add(procKillGrace)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pid, 0) != nil {
			return // the group has no members left - nothing to escalate against
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = signalGroup(pid, syscall.SIGKILL)
}

// signalGroup sends sig to the whole process group led by pid, falling back to
// pid alone if the group send fails (mirrors storage.RunState.Kill).
func signalGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err == nil {
		return nil
	}
	return syscall.Kill(pid, sig)
}

// forwardTerminalSignals keeps the worker reachable from outside pm. Setpgid
// moves it out of pm's process group, so neither the terminal's Ctrl-C nor the
// board's killRun (which signals the manager's group) reaches it any more -
// without forwarding, interrupting or stopping pm would leave a headless worker
// running for the rest of its timeout, holding the worktree lock the board has
// just released. The signal is passed to the worker's group and escalated
// there, and only then re-raised on pm with default handling restored, so pm
// still dies exactly as it did before. The returned func stops forwarding.
func forwardTerminalSignals(pid *atomic.Int64) func() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case s := <-sigc:
			sig, ok := s.(syscall.Signal)
			if !ok {
				return
			}
			stopGroup(int(pid.Load()))
			signal.Stop(sigc)
			_ = syscall.Kill(os.Getpid(), sig)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(sigc)
		close(done)
	}
}
