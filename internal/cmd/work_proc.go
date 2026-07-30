package cmd

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
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

// groupCmd builds an exec.Cmd that runs in its OWN process group, so the whole
// worker tree - not just the direct child - can be signalled. Without it,
// exec.CommandContext's cancellation reaches only the process pm spawned: a
// timed-out worker's test runner / dev server / nested agent survives, keeps
// the inherited stdout pipe open, and the deadline enforces nothing.
//
// Group semantics are the established pattern here - the board's killRun already
// stops a run via storage.RunState.Kill (`kill -pgid`); this is the missing half
// that actually establishes the group.
func groupCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Default cancellation is Process.Kill() - the direct child only. Signal the
	// group instead so descendants go down with it; SIGTERM first so a worker
	// gets to flush, with the SIGKILL escalation in runGroupCmd behind it.
	c.Cancel = func() error { return signalGroup(c.Process, syscall.SIGTERM) }
	c.WaitDelay = procWaitDelay
	return c
}

// runGroupCmd starts c (built by groupCmd), waits for it, and makes sure the
// group leaves nothing behind. Wait's error is returned verbatim - callers
// distinguish a plain non-zero exit (*exec.ExitError) from exec.ErrWaitDelay,
// which means "the process is gone but a descendant still held its output".
func runGroupCmd(c *exec.Cmd) error {
	if err := c.Start(); err != nil {
		return err
	}
	stopForwarding := forwardTerminalSignals(c)
	defer stopForwarding()

	err := c.Wait()
	if err != nil {
		// Abnormal exit - the deadline fired, WaitDelay unblocked us, or the
		// command failed. Any of those can leave descendants running, and with
		// their own process group nothing else will ever collect them. SIGKILL
		// what is left; ESRCH (nothing left, the common case) is ignored.
		_ = signalGroup(c.Process, syscall.SIGKILL)
	}
	return err
}

// signalGroup sends sig to p's whole process group, falling back to p alone if
// the group send fails (mirrors storage.RunState.Kill).
func signalGroup(p *os.Process, sig syscall.Signal) error {
	if p == nil {
		return nil
	}
	if err := syscall.Kill(-p.Pid, sig); err == nil {
		return nil
	}
	return p.Signal(sig)
}

// forwardTerminalSignals keeps Ctrl-C working on an interactive `pm work`.
// Setpgid moves the child out of the terminal's foreground process group, so it
// no longer receives the terminal's SIGINT - without forwarding, interrupting pm
// would leave the worker running as an orphan. The signal is passed to the
// worker's group, then re-raised on pm itself with default handling restored, so
// pm dies exactly as it did before. The returned func stops forwarding.
func forwardTerminalSignals(c *exec.Cmd) func() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case s := <-sigc:
			sig, ok := s.(syscall.Signal)
			if !ok {
				return
			}
			_ = signalGroup(c.Process, sig)
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
