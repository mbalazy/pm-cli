package cmd

import (
	"fmt"
	"io"
	"sync/atomic"
	"syscall"

	"github.com/mbalazy/pm/internal/storage"
)

// The catchable half of "a dead manager says why" (the other half, for deaths
// nothing can catch, is storage.ReconcileCrashedRuns).
//
// A manager killed by SIGINT/SIGTERM/SIGHUP forwards the signal to its worker
// and then re-raises it on itself with the default disposition restored
// (forwardTerminalSignals) - so it dies without ever reaching the code that
// writes its journal end line. Every Ctrl-C, and every SIGTERM from a session
// harness that takes a background process group down, therefore used to leave an
// orphaned `start` indistinguishable from a SIGKILL.
//
// It journals "killed", NOT "crashed", and the distinction is the point: this is
// a kill, just not necessarily one the board issued, and the run-status
// histogram already has the right word for it. The signal name goes in Error.
// The board's own killRun may append its own `killed` line for the same run - the
// two carry the same run_id, which is exactly what makes `pm executor stats`
// count one physical run (see aggregateJournal's duplicate handling).

// crashJournal is the journal identity of the run this process is driving: the
// template line to append if the process is killed, plus where to append it.
type crashJournal struct {
	stateDir string
	entry    storage.JournalEntry
}

// activeCrashJournal holds the armed run. A package-level single slot is enough
// and honest: one pm process drives exactly one top-level run (`pm work`, `pm
// run-epic` or `pm finish`), and a signal handler cannot be passed an argument.
var activeCrashJournal atomic.Pointer[crashJournal]

// armCrashJournal registers the line to journal if this process is killed by a
// catchable signal, and returns the disarm closure. Call it right AFTER the
// run's own start line (there is nothing to close before that) and defer the
// disarm, so a run that reaches its own end line never also reports a kill.
//
// tmpl should be the run's start entry: the event and error are overwritten
// here, everything identifying the run (kind, project, task, run id, pid,
// branch, model) is carried over verbatim, so the killed line pairs with the
// start line by run id.
func armCrashJournal(stateDir string, tmpl storage.JournalEntry) func() {
	cj := &crashJournal{stateDir: stateDir, entry: tmpl}
	cj.entry.Event = storage.JournalEventKilled
	cj.entry.Status = storage.RunStatusFailed
	activeCrashJournal.Store(cj)
	return func() { activeCrashJournal.Store(nil) }
}

// journalTerminalSignal appends the armed run's killed line, naming the signal.
// A no-op when nothing is armed (a `pm work --dry-run`, any non-executor
// command), and it disarms itself so a second signal cannot append a second
// line.
//
// Best-effort and quick on purpose: it runs on the way to re-raising the signal,
// so a slow or failing write must not delay the process's death. Same contract
// as every other journal append - a failure costs history, never correctness.
func journalTerminalSignal(sig syscall.Signal) {
	cj := activeCrashJournal.Swap(nil)
	if cj == nil {
		return
	}
	e := cj.entry
	// The template is the run's START line, TS included, and AppendJournal only
	// stamps an EMPTY TS - so without this reset the killed line carried the
	// start time, and `pm executor stats` reported pm-cli-118's kill 64 minutes
	// before it happened (start 14:32:41Z, kill 15:36:28Z, both lines stamped
	// 14:32:41Z). The kill happens NOW; the identity fields are what pair it
	// with the start line, not the clock.
	e.TS = ""
	e.Error = fmt.Sprintf("manager received %s (signal %d) and re-raised it - the run did not finish; "+
		"whatever the worker had committed is on its branch", signalName(sig), int(sig))
	_ = storage.AppendJournal(cj.stateDir, &e)
}

// signalName spells the three signals the forwarder handles; anything else
// prints as its number, which is still more than the journal used to hold.
func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	default:
		return fmt.Sprintf("signal %d", int(sig))
	}
}

// reportReconciledCrashes reconciles this project's unclosed crashes and says
// what it found. Printed rather than silent: the run about to start is often the
// re-run OF the crash (one such run was re-launched 42 minutes later), and
// that is the moment the reason is worth reading.
func reportReconciledCrashes(w io.Writer, stateDir, label string) {
	for _, e := range storage.ReconcileCrashedRuns(stateDir) {
		fmt.Fprintf(w, "%s: journaled a previously unrecorded crash of %s (run %s): %s\n", label, e.TaskID, e.RunID, e.Error)
	}
}
