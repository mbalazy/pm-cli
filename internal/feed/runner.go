package feed

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Runner runs one external command in dir and returns its stdout. The git
// and github sources take one so a test can fake `git`/`gh` and so `pm
// serve` can hand in the executor's own process-group runner; the default
// below is the same discipline in miniature.
type Runner func(ctx context.Context, dir, name string, args ...string) (string, error)

// CommandTimeout bounds ONE external command. A `gh` call against a slow
// API or a `git log` on a huge repo must never hold the feed - or the
// scheduler behind it - for longer than this.
var CommandTimeout = 30 * time.Second

// DefaultRunner runs the command in its own process group with
// CommandTimeout, killing the whole group on expiry so a `gh` that spawned a
// pager or a credential helper does not outlive the call.
func DefaultRunner(ctx context.Context, dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process != nil {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	c.WaitDelay = 2 * time.Second
	// No terminal, no pager, no prompts: gh in particular would otherwise
	// wait on a tty that is not there.
	c.Env = append(c.Environ(), "GH_PAGER=cat", "PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout.String(), fmt.Errorf("%s timed out after %s", name, CommandTimeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), fmt.Errorf("%s: %w%s", name, err, detail(stderr.String()))
		}
		return stdout.String(), fmt.Errorf("%s: %w", name, err)
	}
	return stdout.String(), nil
}

// detail trims stderr to one line for an error message.
func detail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return ": " + s
}

// ErrNotFound wraps exec.ErrNotFound so a source can say "gh is not
// installed" as a per-project error and move on.
func isNotFound(err error) bool {
	return errors.Is(err, exec.ErrNotFound)
}
