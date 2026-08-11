package storage

import (
	"fmt"
	"time"
)

// A manager that dies without writing its own terminal line leaves a `start`
// with nothing after it, and until now that was the WHOLE record: `pm executor
// stats` could count the shape ("crashed 2") but no byte anywhere said why.
// Three of 36 runs in the 2026-08-11 retro window were exactly that - all on
// 2026-08-08, on two different projects - and the only thing recoverable about
// them was that they had happened.
//
// Deaths split into two kinds and each needs its own answer:
//
//   - CATCHABLE (SIGINT from a terminal, SIGTERM from a session harness or the
//     board's kill): the process is still running when the signal arrives, so it
//     journals its own terminal line naming the signal. That half lives in the
//     cmd package, next to the signal forwarder that already exists for the
//     worker's sake.
//   - UNCATCHABLE (SIGKILL, an OOM kill, a reboot): nothing can run at the
//     moment of death, so the reason is reconstructed AFTERWARDS, from what the
//     dead run left behind - which is what ReconcileCrashedRuns does, and it is
//     the reason the crashed line says what it could ESTABLISH rather than
//     claiming to know the cause.

// ReconcileCrashedRuns closes the journal's orphaned `start` lines for runs that
// are demonstrably dead, appending one `crashed` line per run with the forensics
// its run-state still holds (when it died, on which sub, in which phase).
//
// Called at the start of an executor run, before its own `start` line, so the
// project's history is reconciled by the next thing that touches it rather than
// by a human noticing a count. Returns the entries it appended so the caller can
// say so out loud - a crash that gets recorded silently is only half-visible.
//
// Idempotent BY CONSTRUCTION: the crashed line it writes is itself a terminal
// line, so the run is no longer orphaned and the next call skips it. That, and
// not a marker file, is what makes it safe to call on every run.
//
// Deliberately conservative in three ways, because writing a WRONG crash line
// would be worse than writing none:
//   - a run-state whose pid is still alive (IsLive) is left alone;
//   - a run-state with no RunID (pre-0.27.0) is skipped: without it, pairing
//     falls back to {kind, task, pid}, and a recycled pid could close the wrong
//     run's start line;
//   - it never touches the run-state itself. Recovering the WORK is a separate,
//     already-solved job (see crashRecoveredSubs); this only records history.
func ReconcileCrashedRuns(projectDir string) []JournalEntry {
	entries, err := ReadJournal(projectDir)
	if err != nil || len(entries) == 0 {
		return nil
	}
	open := openRunIDs(entries)
	if len(open) == 0 {
		return nil
	}

	var appended []JournalEntry
	// Both maps: an acceptance run (`pm finish`) crashes the same way and its
	// journal lines are paired by the same rule.
	for _, states := range []map[string]*RunState{ReadRunStates(projectDir), ReadFinishRunStates(projectDir)} {
		for _, st := range states {
			if st == nil || st.RunID == "" || !open[st.RunID] || st.IsLive() {
				continue
			}
			e := JournalEntry{
				Event: JournalEventCrashed, Kind: st.Kind, Project: st.Project, TaskID: st.TaskID,
				RunID: st.RunID, PID: st.PID, Status: RunStatusFailed,
				DurationS: crashRunSeconds(st),
				Error:     describeCrash(st),
			}
			if AppendJournal(projectDir, &e) == nil {
				appended = append(appended, e)
				delete(open, st.RunID) // two states can never share a run id, but do not rely on it
			}
		}
	}
	return appended
}

// openRunIDs returns the run ids whose `start` line has no terminal line of any
// kind. Only ids are considered: see ReconcileCrashedRuns on why an id-less line
// is left alone.
func openRunIDs(entries []JournalEntry) map[string]bool {
	open := map[string]bool{}
	for _, e := range entries {
		if e.RunID == "" {
			continue
		}
		switch e.Event {
		case JournalEventStart:
			open[e.RunID] = true
		case JournalEventEnd, JournalEventKilled, JournalEventCrashed:
			delete(open, e.RunID)
		}
	}
	return open
}

// crashRunSeconds is how long the run had been alive when it was last seen. The
// heartbeat re-stamps Updated every 30s while the manager lives, so this is the
// closest thing to a time of death that survives a SIGKILL - and it is reported
// as the run's duration because that is what it is: the part of the run that
// happened.
func crashRunSeconds(st *RunState) int {
	started, err1 := time.Parse(time.RFC3339, st.Started)
	updated, err2 := time.Parse(time.RFC3339, st.Updated)
	if err1 != nil || err2 != nil || !updated.After(started) {
		return 0
	}
	return int(updated.Sub(started).Seconds())
}

// describeCrash states what the leftovers ESTABLISH, in one line, and names the
// three causes that produce this exact shape rather than pretending to have
// picked one. The last heartbeat is the load-bearing part: it bounds the time of
// death to a 30-second window, which is what makes it possible to line a crash
// up against anything else that happened on the machine (a session harness
// exiting, a reboot, an OOM kill in the system log).
func describeCrash(st *RunState) string {
	where := ""
	if st.CurrentSub != "" {
		where = ", on sub " + st.CurrentSub
	}
	if st.Phase != "" {
		where += " (phase " + st.Phase + ")"
	}
	return fmt.Sprintf(
		"manager died with no terminal line and no signal recorded, so it was uncatchable: SIGKILL, an OOM kill or a reboot. "+
			"Reconstructed from the run-state: pid %d, started %s, last heartbeat %s%s. "+
			"The heartbeat runs every %s while the manager lives, so the death is inside the window after it.",
		st.PID, st.Started, st.Updated, where, HeartbeatInterval)
}
