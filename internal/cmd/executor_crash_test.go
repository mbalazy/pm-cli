package cmd

import (
	"bytes"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func armedStart() storage.JournalEntry {
	return storage.JournalEntry{
		Event: storage.JournalEventStart, Kind: storage.RunKindEpic,
		Project: "proj", TaskID: "proj-1", RunID: "run-1", PID: 4242, Model: "opus", Branch: "epic/proj-1",
	}
}

func TestJournalTerminalSignalWritesAKilledLine(t *testing.T) {
	dir := t.TempDir()
	disarm := armCrashJournal(dir, armedStart())
	defer disarm()

	journalTerminalSignal(syscall.SIGTERM)

	entries, err := storage.ReadJournal(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly one line, got %d (err %v)", len(entries), err)
	}
	e := entries[0]
	if e.Event != storage.JournalEventKilled {
		t.Errorf("event = %q, want killed - a caught signal IS a kill, and the run-status histogram already has that word", e.Event)
	}
	// Everything that identifies the run is carried over from the start line, so
	// the two pair by run id.
	if e.RunID != "run-1" || e.TaskID != "proj-1" || e.Kind != storage.RunKindEpic || e.Branch != "epic/proj-1" {
		t.Errorf("killed line lost the run's identity: %+v", e)
	}
	if !strings.Contains(e.Error, "SIGTERM") || !strings.Contains(e.Error, "15") {
		t.Errorf("error should name the signal and its number, got %q", e.Error)
	}
}

// TestJournalTerminalSignalStampsTheKillNotTheStart: the armed template is the
// run's start line WITH its TS, and AppendJournal stamps only an empty TS - so
// the killed line used to inherit the start time (pm-cli-118: start and kill
// both journaled as 14:32:41Z, the kill really at 15:36:28Z). The kill is now.
func TestJournalTerminalSignalStampsTheKillNotTheStart(t *testing.T) {
	dir := t.TempDir()
	start := armedStart()
	start.TS = "2026-09-02T14:32:41Z"
	defer armCrashJournal(dir, start)()

	journalTerminalSignal(syscall.SIGTERM)

	entries, err := storage.ReadJournal(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly one line, got %d (err %v)", len(entries), err)
	}
	e := entries[0]
	if e.TS == start.TS {
		t.Fatalf("killed line carries the START stamp %s - it must be stamped when the signal arrives", e.TS)
	}
	ts, perr := time.Parse(time.RFC3339, e.TS)
	if perr != nil {
		t.Fatalf("killed ts %q is not RFC3339: %v", e.TS, perr)
	}
	if d := time.Since(ts); d < 0 || d > time.Minute {
		t.Errorf("killed ts %s is not 'now' (off by %s)", e.TS, d)
	}
}

// TestJournalTerminalSignalOnlyOnce: the handler can fire again (a second
// Ctrl-C), and the run must not gain two terminal lines from one death.
func TestJournalTerminalSignalOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	defer armCrashJournal(dir, armedStart())()

	journalTerminalSignal(syscall.SIGINT)
	journalTerminalSignal(syscall.SIGINT)

	entries, _ := storage.ReadJournal(dir)
	if len(entries) != 1 {
		t.Fatalf("want 1 killed line after two signals, got %d", len(entries))
	}
}

// TestJournalTerminalSignalDisarmed: a run that reached its own end line has
// already said how it went. A killed line after that would be a second terminal
// line for a run that finished.
func TestJournalTerminalSignalDisarmed(t *testing.T) {
	dir := t.TempDir()
	armCrashJournal(dir, armedStart())()

	journalTerminalSignal(syscall.SIGTERM)

	if entries, _ := storage.ReadJournal(dir); len(entries) != 0 {
		t.Fatalf("a disarmed run must journal nothing, got %+v", entries)
	}
}

// TestReportReconciledCrashesSaysWhatItFound: the reconcile happens at the start
// of a run that is frequently the RE-RUN of the crash, so the reason has to be
// on screen, not only in the file.
func TestReportReconciledCrashesSaysWhatItFound(t *testing.T) {
	dir := t.TempDir()
	if err := storage.WriteRunState(dir, &storage.RunState{
		TaskID: "proj-1", RunID: "run-dead", Project: "proj", Kind: storage.RunKindEpic,
		Status: storage.RunStatusRunning, PID: 2147483646, Started: "2026-08-08T17:45:56Z",
		CurrentSub: "proj-1-2", Phase: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.AppendJournal(dir, &storage.JournalEntry{
		Event: storage.JournalEventStart, Kind: storage.RunKindEpic, Project: "proj", TaskID: "proj-1",
		RunID: "run-dead", PID: 2147483646,
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	reportReconciledCrashes(&out, dir, "pm run-epic")
	got := out.String()
	for _, want := range []string{"proj-1", "run-dead", "SIGKILL"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}

	// A second run says nothing: the crash is history now, not news.
	out.Reset()
	reportReconciledCrashes(&out, dir, "pm run-epic")
	if out.Len() != 0 {
		t.Errorf("an already-reconciled crash must not be re-reported:\n%s", out.String())
	}
}

// TestStatsReportsCrashReasons closes the loop the whole change exists for:
// `pm executor stats` used to be able to COUNT unexplained crashes and nothing
// more.
func TestStatsReportsCrashReasons(t *testing.T) {
	st := aggregateJournal([]storage.JournalEntry{
		{Event: storage.JournalEventStart, TS: "2026-08-08T06:29:00Z", Kind: "run-epic", TaskID: "pm-cli-102", RunID: "r1", PID: 29482},
		{Event: storage.JournalEventCrashed, TS: "2026-08-08T07:00:00Z", Kind: "run-epic", TaskID: "pm-cli-102", RunID: "r1", PID: 29482,
			Status: storage.RunStatusFailed, Error: "manager died with no terminal line and no signal recorded"},
		{Event: storage.JournalEventStart, TS: "2026-08-08T10:19:05Z", Kind: "run-epic", TaskID: "pm-cli-100", RunID: "r2", PID: 74450},
		{Event: storage.JournalEventKilled, TS: "2026-08-08T10:25:05Z", Kind: "run-epic", TaskID: "pm-cli-100", RunID: "r2", PID: 74450,
			Status: storage.RunStatusFailed, Error: "manager received SIGTERM (signal 15) and re-raised it"},
	}, func(int, time.Time) bool { return false }, testNow)

	if st.Crashes != 1 {
		t.Errorf("Crashes = %d, want 1 (the crashed line, not the killed one)", st.Crashes)
	}
	if len(st.Deaths) != 2 {
		t.Fatalf("Deaths = %d, want both the crash and the kill", len(st.Deaths))
	}

	out := renderJournalStats("pm-cli", "/tmp/j.jsonl", st)
	for _, want := range []string{
		"why runs ended abnormally (2 recorded)",
		"crashed pm-cli-102",
		"no signal recorded",
		"killed pm-cli-100",
		"SIGTERM",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output missing %q:\n%s", want, out)
		}
	}
	// A crashed line is a terminal line, so its start is no longer an orphan and
	// the run must be counted exactly once.
	if !strings.Contains(out, "## Runs: 2") {
		t.Errorf("crashed/killed lines must not double-count their runs:\n%s", out)
	}
}

// TestStatsDeathsDeduplicateBoardKills: one board kill produces TWO killed
// lines for the same physical death (the dying manager's own, then killRun's,
// sharing a run id). The histogram already counts them once; the deaths report
// must list them once too, or a few board kills push genuine crash reasons off
// its display cap.
func TestStatsDeathsDeduplicateBoardKills(t *testing.T) {
	st := aggregateJournal([]storage.JournalEntry{
		{Event: storage.JournalEventStart, TS: "2026-08-11T09:00:00Z", Kind: "run-epic", TaskID: "p-1", RunID: "r1", PID: 10},
		{Event: storage.JournalEventKilled, TS: "2026-08-11T09:30:00Z", Kind: "run-epic", TaskID: "p-1", RunID: "r1", PID: 10,
			Status: storage.RunStatusFailed, Error: "manager received SIGTERM (signal 15) and re-raised it"},
		{Event: storage.JournalEventKilled, TS: "2026-08-11T09:30:02Z", Kind: "run-epic", TaskID: "p-1", RunID: "r1", PID: 10,
			Status: storage.RunStatusFailed, Error: "stopped by user"},
	}, func(int, time.Time) bool { return false }, testNow)

	if len(st.Deaths) != 1 {
		t.Fatalf("Deaths = %d, want 1 - two killed lines with one run id are one physical death", len(st.Deaths))
	}
	if !strings.Contains(st.Deaths[0].Reason, "SIGTERM") {
		t.Errorf("the first (self-journaled) line should be the one listed, got %q", st.Deaths[0].Reason)
	}
}
