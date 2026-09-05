package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// rigTimeout caps executor.rig. A WARM start of a runtime (boot a simulator,
// start a bundler, launch the app, prove the tree reaches it) is minutes; a
// COLD start (a native build) is 15-40 min on the sim-rig journal's record and
// is deliberately NOT allowed inside a run - it is done by hand once per slot.
// The cap is what enforces that: a rig command that builds runs into it and
// reports DEAD, which the human reads as "build the slot first". A var so
// tests can shrink it.
var rigTimeout = 10 * time.Minute

// rigOutputCap bounds how much of the rig command's output reaches the prompt
// (the TAIL - a rig checker prints its verdict last).
const rigOutputCap = 4000

// Journal words for JournalEntry.Rig: what the rig check actually found.
const (
	rigWordUp   = "up"
	rigWordDead = "dead"
)

// rigVerdict is the outcome of one executor.rig run: up, or dead with why.
// It is a value the run keeps (journal, dry-run, run log) and renders ONCE into
// a prompt section (rigSection) for every runtime-enabled worker of the run.
type rigVerdict struct {
	Command string
	Up      bool
	// Reason says why the rig is dead, in one line: "exited 1", "timed out
	// after 10m0s", "could not run: ...". Empty when Up.
	Reason string
	// Output is the tail of the command's combined output (both verdicts -
	// a rig checker's "RIG OK, marker seen after 4s" is worth having too).
	Output string
}

// journalWord is what JournalEntry.Rig records for this verdict.
func (v rigVerdict) journalWord() string {
	if v.Up {
		return rigWordUp
	}
	return rigWordDead
}

// String is the one-line form for the run log and the dry-run.
func (v rigVerdict) String() string {
	if v.Up {
		return "RIG UP"
	}
	return "RIG DEAD (" + v.Reason + ")"
}

// runRig runs the project's executor.rig command in dir with env appended to
// the process environment (the claimed slot's env - SIM_UDID, the port - is
// exactly what a rig check needs and exactly what prepare does not get). It
// never returns an error: every way the command can fail is a DEAD verdict
// with the reason, because a dead rig is a handoff for the worker to record,
// not a reason to abort a run that has already paid for its prepare.
func runRig(errOut io.Writer, dir, command string, env []string) rigVerdict {
	v := rigVerdict{Command: command}
	ctx, cancel := context.WithTimeout(context.Background(), rigTimeout)
	defer cancel()
	// Same process-group enforcement as the baseline: a rig command that
	// starts a bundler in the background must not hold the output pipe open
	// past its own cap. Unlike the baseline, a background process the command
	// leaves behind ON PURPOSE (Metro) is part of the point - which is why the
	// command is expected to detach such things itself (nohup, setsid); what
	// stays attached to our pipe is killed at the cap.
	c := groupCmd(ctx, "sh", "-c", command)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	var combined bytes.Buffer
	c.Stdout = &combined
	c.Stderr = &combined
	err := runGroupCmd(ctx, c, nil)
	v.Output = tailOutput(combined.String(), rigOutputCap)
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		v.Reason = fmt.Sprintf("timed out after %s", rigTimeout)
	case err == nil || errors.Is(err, exec.ErrWaitDelay):
		// ErrWaitDelay = exit 0 with leftovers holding the pipe (see
		// captureBaseline) - the verdict is the exit status, and it is 0.
		v.Up = true
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			v.Reason = fmt.Sprintf("exited %d", exitErr.ExitCode())
		} else {
			v.Reason = "could not run: " + err.Error()
		}
	}
	fmt.Fprintf(errOut, "pm: rig check in %s: %s -> %s\n", dir, command, v)
	return v
}

// tailOutput keeps the last limit bytes of s, marking the cut.
func tailOutput(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return fmt.Sprintf("(... first %d bytes truncated ...)\n%s", len(s)-limit, s[len(s)-limit:])
}

// rigSection renders the "## Runtime rig" prompt section for a
// runtime-enabled worker. It is the ONLY thing that tells the worker whether
// the runtime phase may drive anything: UP = drive it as bound, DEAD = do not
// try, record the handoff. The worker never re-checks or re-starts the rig
// itself - the check ran once for the run and a worker that "fixes" a rig
// burns its budget on a machine problem it cannot see.
func rigSection(v rigVerdict) string {
	var sb strings.Builder
	sb.WriteString("\n## Runtime rig (checked ONCE for this run, BEFORE your worker started)\n")
	if v.Up {
		fmt.Fprintf(&sb, "`%s` exited 0 - the rig is UP: the slot's runtime is running the tree you work on. The `runtime` phase may drive it as bound. Do not re-run this check and never restart, rebuild or re-point the runtime yourself; if it stops answering mid-phase, record `TODO: runtime verification incomplete - rig stopped answering: <what you saw>` in `unresolved` and move on.\n", v.Command)
	} else {
		fmt.Fprintf(&sb, "`%s` %s - the rig is DEAD: nothing is proven to run your tree. SKIP the `runtime` phase entirely - do not boot, build, install or launch anything, do not probe the runtime by other means, and do not spend a single turn on it. Record exactly one line in `unresolved`: `TODO: runtime verification not run - rig DEAD: %s` (append the gist of the output below if it names a cause). Your verify verdict is unaffected: the gate is verify, not the rig.\n", v.Command, v.Reason, v.Reason)
	}
	if v.Output != "" {
		fmt.Fprintf(&sb, "\n```\n%s\n```\n", v.Output)
	}
	return sb.String()
}

// runtimeSubs returns the subs of a run that would drive the runtime phase:
// runtime-enabled, not already landed, not a manual gate. Zero of them means
// the run has no reason to touch the rig at all - a run with no runtime sub
// pays nothing for the feature, which is the pm-cli-122 acceptance criterion.
func runtimeSubs(subs []*storage.Task, doneStatus storage.TaskStatus) []*storage.Task {
	var out []*storage.Task
	for _, s := range subs {
		if !s.Meta.RuntimeEnabled() || s.Meta.Mode == "manual" {
			continue
		}
		if s.Meta.Status == doneStatus || s.Meta.Status == storage.StatusDone {
			continue
		}
		out = append(out, s)
	}
	return out
}
