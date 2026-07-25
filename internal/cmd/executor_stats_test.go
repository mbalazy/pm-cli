package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// journalFixture writes entries into a temp project dir via the REAL append path
// (AppendJournal), so the JSONL the test reads back is byte-for-byte what the
// executor writes. Returns the project dir.
func journalFixture(t *testing.T, entries ...storage.JournalEntry) string {
	t.Helper()
	dir := t.TempDir()
	for i := range entries {
		if err := storage.AppendJournal(dir, &entries[i]); err != nil {
			t.Fatalf("append journal entry %d: %v", i, err)
		}
	}
	return dir
}

func startEntry(kind, taskID string, pid int) storage.JournalEntry {
	return storage.JournalEntry{Event: storage.JournalEventStart, Kind: kind, TaskID: taskID, PID: pid}
}

// noneAlive is the default liveness probe for the table: every unpaired start
// is a crash. Cases that want the live-run branch pass their own.
func noneAlive(int) bool { return false }

// testNow is the fixed "now" the table aggregates against. AppendJournal stamps
// TS with the real wall clock, so a far-future reference would push every
// fixture entry outside liveWindow; this sits close enough to real time that
// fixtures count as recent, and cases about staleness set TS explicitly.
var testNow = time.Now()

func TestAggregateJournal(t *testing.T) {
	tests := []struct {
		name    string
		entries []storage.JournalEntry
		alive   func(int) bool // nil -> noneAlive
		want    journalStats
	}{
		{
			name:    "empty journal",
			entries: nil,
			want:    journalStats{},
		},
		{
			name: "matched work run",
			entries: []storage.JournalEntry{
				startEntry("work", "app-1", 100),
				{
					Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-1", PID: 100,
					Status: "done", DurationS: 600,
					Subs: []storage.JournalSub{{ID: "app-1", Result: "merged", DurationS: 590, Turns: 42, CostUSD: 1.5}},
				},
			},
			want: journalStats{
				Runs:        1,
				Kinds:       map[string]*kindStats{"work": {Runs: 1, Statuses: map[string]int{"done": 1}, DurationS: numStat{Total: 600, Samples: 1}}},
				Subs:        1,
				SubResults:  map[string]int{"merged": 1},
				SubDuration: numStat{Total: 590, Samples: 1},
				Turns:       numStat{Total: 42, Samples: 1},
				Cost:        numStat{Total: 1.5, Samples: 1},
			},
		},
		{
			name: "epic run with mixed subs - skips carry zeros",
			entries: []storage.JournalEntry{
				startEntry("run-epic", "app-9", 200),
				{
					Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-9", PID: 200,
					Status: "done", DurationS: 3600,
					Subs: []storage.JournalSub{
						{ID: "app-9-1", Result: "merged", DurationS: 1000, Turns: 60, CostUSD: 2},
						{ID: "app-9-2", Result: "blocked", DurationS: 500, Turns: 40, CostUSD: 1},
						{ID: "app-9-3", Result: "skipped"},
						{ID: "app-9-4", Result: "manual"},
					},
				},
			},
			want: journalStats{
				Runs:        1,
				Kinds:       map[string]*kindStats{"run-epic": {Runs: 1, Statuses: map[string]int{"done": 1}, DurationS: numStat{Total: 3600, Samples: 1}}},
				Subs:        4,
				SubResults:  map[string]int{"merged": 1, "blocked": 1, "skipped": 1, "manual": 1},
				SubDuration: numStat{Total: 1500, Samples: 2},
				Turns:       numStat{Total: 100, Samples: 2},
				Cost:        numStat{Total: 3, Samples: 2},
			},
		},
		{
			name: "killed run",
			entries: []storage.JournalEntry{
				startEntry("run-epic", "app-5", 300),
				{Event: storage.JournalEventKilled, Kind: "run-epic", TaskID: "app-5", PID: 300},
			},
			want: journalStats{
				Runs:       1,
				Kinds:      map[string]*kindStats{"run-epic": {Runs: 1, Statuses: map[string]int{runStatusKilled: 1}}},
				SubResults: map[string]int{},
			},
		},
		{
			name:    "crash - start with no terminal line",
			entries: []storage.JournalEntry{startEntry("work", "app-2", 400)},
			want: journalStats{
				Runs:       1,
				Crashes:    1,
				Kinds:      map[string]*kindStats{"work": {Runs: 1, Statuses: map[string]int{runStatusCrashed: 1}}},
				SubResults: map[string]int{},
			},
		},
		{
			// Two starts, one end: FIFO closes one and leaves the other a
			// crash. (This shape does NOT prove PID discrimination - the case
			// below does; the counter alone gives the same answer here.)
			name: "same task run twice, only one terminates",
			entries: []storage.JournalEntry{
				startEntry("work", "app-3", 500),
				startEntry("work", "app-3", 501),
				{Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-3", PID: 501, Status: "failed", DurationS: 120},
			},
			want: journalStats{
				Runs: 2,
				Kinds: map[string]*kindStats{"work": {
					Runs:      2,
					Statuses:  map[string]int{"failed": 1, runStatusCrashed: 1},
					DurationS: numStat{Total: 120, Samples: 1},
				}},
				Crashes:    1,
				SubResults: map[string]int{},
			},
		},
		{
			name: "mixed kinds plus a terminal line with no start",
			entries: []storage.JournalEntry{
				startEntry("work", "app-1", 100),
				{Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-1", PID: 100, Status: "done", DurationS: 60},
				// orphan end (history truncated before its start line)
				{Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-8", PID: 999, Status: "done", DurationS: 30},
			},
			want: journalStats{
				Runs: 2,
				Kinds: map[string]*kindStats{
					"work":     {Runs: 1, Statuses: map[string]int{"done": 1}, DurationS: numStat{Total: 60, Samples: 1}},
					"run-epic": {Runs: 1, Statuses: map[string]int{"done": 1}, DurationS: numStat{Total: 30, Samples: 1}},
				},
				SubResults: map[string]int{},
			},
		},
		{
			// The pairing key includes the PID, so a terminal line whose PID
			// does NOT match the start cannot close it. Without the PID in the
			// key this would collapse to 1 run / 0 crashes.
			name: "terminal line with a different pid does not close the start",
			entries: []storage.JournalEntry{
				startEntry("work", "app-3", 500),
				{Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-3", PID: 501, Status: "failed", DurationS: 120},
			},
			want: journalStats{
				Runs: 2,
				Kinds: map[string]*kindStats{"work": {
					Runs:      2,
					Statuses:  map[string]int{"failed": 1, runStatusCrashed: 1},
					DurationS: numStat{Total: 120, Samples: 1},
				}},
				Crashes:    1,
				SubResults: map[string]int{},
			},
		},
		{
			// Live holder pid -> the run is in flight, not a crash. `pm
			// executor stats` is routinely run mid-epic.
			name:    "unpaired start with a live pid is running, not crashed",
			entries: []storage.JournalEntry{startEntry("run-epic", "app-7", 700)},
			alive:   func(pid int) bool { return pid == 700 },
			want: journalStats{
				Runs:       1,
				Running:    1,
				Kinds:      map[string]*kindStats{"run-epic": {Runs: 1, Statuses: map[string]int{runStatusRunning: 1}}},
				SubResults: map[string]int{},
			},
		},
		{
			// The kill race: run_epic writes "end", then keeps working (PR
			// creation) and the board kills it, appending "killed" for the SAME
			// run. That is one run, not two.
			name: "end followed by killed for the same run counts once",
			entries: []storage.JournalEntry{
				startEntry("run-epic", "app-6", 600),
				{
					Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-6", PID: 600,
					Status: "done", DurationS: 3600,
					Subs: []storage.JournalSub{{ID: "app-6-1", Result: "merged", Turns: 10, CostUSD: 1}},
				},
				{Event: storage.JournalEventKilled, Kind: "run-epic", TaskID: "app-6", PID: 600},
			},
			want: journalStats{
				Runs:       1,
				Kinds:      map[string]*kindStats{"run-epic": {Runs: 1, Statuses: map[string]int{"done": 1}, DurationS: numStat{Total: 3600, Samples: 1}}},
				Subs:       1,
				SubResults: map[string]int{"merged": 1},
				Turns:      numStat{Total: 10, Samples: 1},
				Cost:       numStat{Total: 1, Samples: 1},
			},
		},
		{
			// The real orbit journal opens with exactly this shape.
			name:    "orphan killed line with no start",
			entries: []storage.JournalEntry{{Event: storage.JournalEventKilled, Kind: "run-epic", TaskID: "app-0", PID: 900}},
			want: journalStats{
				Runs:       1,
				Kinds:      map[string]*kindStats{"run-epic": {Runs: 1, Statuses: map[string]int{runStatusKilled: 1}}},
				SubResults: map[string]int{},
			},
		},
		{
			// One journal holding a completed run, a killed run and a crash at
			// once - the shape where FIFO pairing could mis-attribute.
			name: "completed, killed and crashed runs in one journal",
			entries: []storage.JournalEntry{
				startEntry("work", "app-1", 100),
				startEntry("run-epic", "app-5", 300),
				startEntry("work", "app-2", 400), // never terminated -> crash
				{
					Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-1", PID: 100,
					Status: "done", DurationS: 600,
					Subs: []storage.JournalSub{{ID: "app-1", Result: "merged", DurationS: 590, Turns: 42, CostUSD: 1.5}},
				},
				{Event: storage.JournalEventKilled, Kind: "run-epic", TaskID: "app-5", PID: 300},
			},
			want: journalStats{
				Runs:    3,
				Crashes: 1,
				Kinds: map[string]*kindStats{
					"work": {
						Runs:      2,
						Statuses:  map[string]int{"done": 1, runStatusCrashed: 1},
						DurationS: numStat{Total: 600, Samples: 1},
					},
					"run-epic": {Runs: 1, Statuses: map[string]int{runStatusKilled: 1}},
				},
				Subs:        1,
				SubResults:  map[string]int{"merged": 1},
				SubDuration: numStat{Total: 590, Samples: 1},
				Turns:       numStat{Total: 42, Samples: 1},
				Cost:        numStat{Total: 1.5, Samples: 1},
			},
		},
		{
			// A run that failed within a second records DurationS 0. That is a
			// real measurement and must stay in the run-level denominator,
			// unlike a sub's absent turns/cost.
			name: "zero-duration end line still counts as a run duration sample",
			entries: []storage.JournalEntry{
				startEntry("work", "app-fast", 800),
				{Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-fast", PID: 800, Status: "failed", DurationS: 0},
				startEntry("work", "app-slow", 801),
				{Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-slow", PID: 801, Status: "done", DurationS: 100},
			},
			want: journalStats{
				Runs: 2,
				Kinds: map[string]*kindStats{"work": {
					Runs:      2,
					Statuses:  map[string]int{"done": 1, "failed": 1},
					DurationS: numStat{Total: 100, Samples: 2}, // both runs, not just the non-zero one
				}},
				SubResults: map[string]int{},
			},
		},
		{
			// Reverse kill race: the SIGTERM fails to land, the manager runs to
			// completion and appends "end" AFTER the board's "killed". Still one
			// run - and the end line's subs/duration must not be thrown away
			// just because the killed line arrived first.
			name: "killed followed by end folds the end line's payload in",
			entries: []storage.JournalEntry{
				startEntry("run-epic", "app-6", 600),
				{Event: storage.JournalEventKilled, Kind: "run-epic", TaskID: "app-6", PID: 600},
				{
					Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-6", PID: 600,
					Status: "done", DurationS: 3600,
					Subs: []storage.JournalSub{{ID: "app-6-1", Result: "merged", Turns: 10, CostUSD: 1}},
				},
			},
			want: journalStats{
				Runs: 1,
				Kinds: map[string]*kindStats{"run-epic": {
					Runs: 1,
					// killed won the status (it closed the run first)...
					Statuses: map[string]int{runStatusKilled: 1},
					// ...but the end line's duration is still recorded
					DurationS: numStat{Total: 3600, Samples: 1},
				}},
				Subs:       1,
				SubResults: map[string]int{"merged": 1},
				Turns:      numStat{Total: 10, Samples: 1},
				Cost:       numStat{Total: 1, Samples: 1},
			},
		},
		{
			// Two ends in a row is NOT the kill race - it is two separate runs
			// that reused the pid, the second one's start append having been
			// lost. Same event type twice => count both.
			name: "two end lines for one start are two runs, not a duplicate",
			entries: []storage.JournalEntry{
				startEntry("work", "app-1", 100),
				{Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-1", PID: 100, Status: "done", DurationS: 60},
				{Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-1", PID: 100, Status: "failed", DurationS: 20},
			},
			want: journalStats{
				Runs: 2,
				Kinds: map[string]*kindStats{"work": {
					Runs:      2,
					Statuses:  map[string]int{"done": 1, "failed": 1},
					DurationS: numStat{Total: 80, Samples: 2},
				}},
				SubResults: map[string]int{},
			},
		},
		{
			// A live-looking pid on an ancient start is pid REUSE, not a run in
			// flight. Without the liveWindow bound this reports running:1 and
			// silently loses a crash.
			name: "stale start with a live pid is a crash, not running",
			entries: []storage.JournalEntry{{
				Event: storage.JournalEventStart, Kind: "run-epic", TaskID: "app-old", PID: 700,
				TS: testNow.Add(-liveWindow - time.Hour).UTC().Format(time.RFC3339),
			}},
			alive: func(pid int) bool { return pid == 700 },
			want: journalStats{
				Runs:       1,
				Crashes:    1,
				Kinds:      map[string]*kindStats{"run-epic": {Runs: 1, Statuses: map[string]int{runStatusCrashed: 1}}},
				SubResults: map[string]int{},
			},
		},
		{
			// Two open starts under ONE key: a pid maps to at most one live
			// process, so at most one of them can be running. Applying a single
			// recency verdict to both would report 2 running / 0 crashed.
			name: "two open starts on one live pid - only the newest is running",
			entries: []storage.JournalEntry{
				{
					Event: storage.JournalEventStart, Kind: "run-epic", TaskID: "app-x", PID: 700,
					TS: testNow.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339), // ancient
				},
				{
					Event: storage.JournalEventStart, Kind: "run-epic", TaskID: "app-x", PID: 700,
					TS: testNow.Add(-5 * time.Minute).UTC().Format(time.RFC3339), // in flight
				},
			},
			alive: func(pid int) bool { return pid == 700 },
			want: journalStats{
				Runs:    2,
				Running: 1,
				Crashes: 1,
				Kinds: map[string]*kindStats{"run-epic": {
					Runs:     2,
					Statuses: map[string]int{runStatusRunning: 1, runStatusCrashed: 1},
				}},
				SubResults: map[string]int{},
			},
		},
		{
			// Both open starts are recent, so recency alone cannot separate
			// them - only the "one pid, at most one live process" rule can.
			// Without it this reports 2 running for a single live process.
			name: "two recent open starts on one live pid - still only one running",
			entries: []storage.JournalEntry{
				{
					Event: storage.JournalEventStart, Kind: "run-epic", TaskID: "app-z", PID: 700,
					TS: testNow.Add(-20 * time.Minute).UTC().Format(time.RFC3339),
				},
				{
					Event: storage.JournalEventStart, Kind: "run-epic", TaskID: "app-z", PID: 700,
					TS: testNow.Add(-5 * time.Minute).UTC().Format(time.RFC3339),
				},
			},
			alive: func(pid int) bool { return pid == 700 },
			want: journalStats{
				Runs:    2,
				Running: 1,
				Crashes: 1,
				Kinds: map[string]*kindStats{"run-epic": {
					Runs:     2,
					Statuses: map[string]int{runStatusRunning: 1, runStatusCrashed: 1},
				}},
				SubResults: map[string]int{},
			},
		},
		{
			// Pins the FIFO pop: the end line closes the OLDEST open start, so
			// what stays open is the RECENT one -> running. A LIFO pop would
			// leave the ancient start open and report a crash instead.
			name: "end closes the oldest start, leaving the recent one running",
			entries: []storage.JournalEntry{
				{
					Event: storage.JournalEventStart, Kind: "run-epic", TaskID: "app-y", PID: 700,
					TS: testNow.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339),
				},
				{
					Event: storage.JournalEventStart, Kind: "run-epic", TaskID: "app-y", PID: 700,
					TS: testNow.Add(-5 * time.Minute).UTC().Format(time.RFC3339),
				},
				{Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-y", PID: 700, Status: "done", DurationS: 50},
			},
			alive: func(pid int) bool { return pid == 700 },
			want: journalStats{
				Runs:    2,
				Running: 1,
				Kinds: map[string]*kindStats{"run-epic": {
					Runs:      2,
					Statuses:  map[string]int{"done": 1, runStatusRunning: 1},
					DurationS: numStat{Total: 50, Samples: 1},
				}},
				SubResults: map[string]int{},
			},
		},
		{
			name: "missing status and result degrade to unknown",
			entries: []storage.JournalEntry{
				startEntry("work", "app-4", 600),
				{
					Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-4", PID: 600,
					Subs: []storage.JournalSub{{ID: "app-4"}},
				},
			},
			want: journalStats{
				Runs: 1,
				Kinds: map[string]*kindStats{"work": {
					Runs:     1,
					Statuses: map[string]int{runStatusUnknown: 1},
					// end line present, so its (absent -> 0) duration is a sample
					DurationS: numStat{Total: 0, Samples: 1},
				}},
				Subs:       1,
				SubResults: map[string]int{runStatusUnknown: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := journalFixture(t, tt.entries...)
			entries, err := storage.ReadJournal(dir)
			if err != nil {
				t.Fatalf("read journal: %v", err)
			}
			if len(entries) != len(tt.entries) {
				t.Fatalf("round-trip lost entries: wrote %d, read %d", len(tt.entries), len(entries))
			}

			alive := tt.alive
			if alive == nil {
				alive = noneAlive
			}
			got := aggregateJournal(entries, alive, testNow)

			if got.Entries != len(tt.entries) {
				t.Errorf("Entries = %d, want %d", got.Entries, len(tt.entries))
			}
			if got.Runs != tt.want.Runs {
				t.Errorf("Runs = %d, want %d", got.Runs, tt.want.Runs)
			}
			if got.Crashes != tt.want.Crashes {
				t.Errorf("Crashes = %d, want %d", got.Crashes, tt.want.Crashes)
			}
			if got.Running != tt.want.Running {
				t.Errorf("Running = %d, want %d", got.Running, tt.want.Running)
			}
			if got.Subs != tt.want.Subs {
				t.Errorf("Subs = %d, want %d", got.Subs, tt.want.Subs)
			}
			assertCounts(t, "SubResults", got.SubResults, tt.want.SubResults)
			assertNumStat(t, "SubDuration", got.SubDuration, tt.want.SubDuration)
			assertNumStat(t, "Turns", got.Turns, tt.want.Turns)
			assertNumStat(t, "Cost", got.Cost, tt.want.Cost)

			if len(got.Kinds) != len(tt.want.Kinds) {
				t.Fatalf("Kinds = %v, want %v", kindNames(got.Kinds), kindNames(tt.want.Kinds))
			}
			for name, want := range tt.want.Kinds {
				k, ok := got.Kinds[name]
				if !ok {
					t.Errorf("missing kind %q (got %v)", name, kindNames(got.Kinds))
					continue
				}
				if k.Runs != want.Runs {
					t.Errorf("kind %s Runs = %d, want %d", name, k.Runs, want.Runs)
				}
				assertCounts(t, "kind "+name+" Statuses", k.Statuses, want.Statuses)
				assertNumStat(t, "kind "+name+" DurationS", k.DurationS, want.DurationS)
			}
		})
	}
}

func assertCounts(t *testing.T, label string, got, want map[string]int) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s[%q] = %d, want %d", label, k, got[k], v)
		}
	}
	for k, v := range got {
		if want[k] != v {
			t.Errorf("%s has unexpected %q = %d", label, k, v)
		}
	}
}

func assertNumStat(t *testing.T, label string, got, want numStat) {
	t.Helper()
	if got.Total != want.Total || got.Samples != want.Samples {
		t.Errorf("%s = {total %v, samples %d}, want {total %v, samples %d}",
			label, got.Total, got.Samples, want.Total, want.Samples)
	}
}

func kindNames(kinds map[string]*kindStats) []string {
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	return out
}

func TestNumStatAvgSkipsZeroSamples(t *testing.T) {
	var n numStat
	if got := n.avg(); got != 0 {
		t.Errorf("avg of empty numStat = %v, want 0 (no divide by zero)", got)
	}
	n.add(0) // a skipped sub: contributes nothing, must not count as a sample
	n.add(10)
	n.add(20)
	if n.Samples != 2 {
		t.Errorf("Samples = %d, want 2 (zero values are not samples)", n.Samples)
	}
	if got := n.avg(); got != 15 {
		t.Errorf("avg = %v, want 15", got)
	}
}

func TestOrderedKeysIsDeterministic(t *testing.T) {
	counts := map[string]int{"weird": 1, "merged": 2, "manual": 1, "blocked": 3, "another": 1}
	want := []string{"merged", "blocked", "manual", "another", "weird"}
	for i := 0; i < 20; i++ { // map iteration order varies per run
		got := orderedKeys(counts, subResultOrder, false)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("orderedKeys = %v, want %v", got, want)
		}
	}

	t.Run("keepZero false omits zero counts", func(t *testing.T) {
		if got := orderedKeys(map[string]int{"merged": 0}, subResultOrder, false); len(got) != 0 {
			t.Errorf("zero counts should be omitted, got %v", got)
		}
	})

	t.Run("keepZero true emits the whole vocabulary", func(t *testing.T) {
		got := orderedKeys(map[string]int{"merged": 1}, subResultOrder, true)
		if strings.Join(got, ",") != strings.Join(subResultOrder, ",") {
			t.Errorf("orderedKeys = %v, want the full order %v", got, subResultOrder)
		}
	})

	t.Run("keepZero true still drops zero keys outside the order", func(t *testing.T) {
		got := orderedKeys(map[string]int{"ghost": 0, "merged": 1}, subResultOrder, true)
		for _, k := range got {
			if k == "ghost" {
				t.Errorf("unknown key with a zero count should not be emitted, got %v", got)
			}
		}
	})
}

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		secs float64
		want string
	}{
		{0, "0s"},
		{45, "45s"},
		{59.6, "1m00s"},
		{432, "7m12s"},
		{3600, "1h00m"},
		{3864, "1h04m"},
	}
	for _, tt := range tests {
		if got := fmtDuration(tt.secs); got != tt.want {
			t.Errorf("fmtDuration(%v) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}

func TestRenderJournalStats(t *testing.T) {
	st := aggregateJournal([]storage.JournalEntry{
		startEntry("run-epic", "app-9", 200),
		{
			Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-9", PID: 200,
			Status: "done", DurationS: 3600,
			Subs: []storage.JournalSub{
				{ID: "app-9-1", Result: "merged", DurationS: 1000, Turns: 60, CostUSD: 2},
				{ID: "app-9-2", Result: "skipped"},
			},
		},
		// two more run-epic runs so the per-kind status list has >1 entry and
		// its ORDER is observable
		startEntry("run-epic", "app-9", 201),
		{Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-9", PID: 201, Status: "failed", DurationS: 100},
		startEntry("run-epic", "app-9", 202),
		{Event: storage.JournalEventKilled, Kind: "run-epic", TaskID: "app-9", PID: 202},
		startEntry("work", "app-2", 400), // crash
	}, noneAlive, testNow)
	out := renderJournalStats("app", "/tmp/app/.executor/journal.jsonl", st)

	for _, want := range []string{
		"# executor stats: app",
		"/tmp/app/.executor/journal.jsonl (7 parsed entries)",
		"## Runs: 4",
		"crashed 1 (start with no end/killed line)",
		"## Subs: 2",
		"turns     60 total, 60.0 avg (1 sub(s))",
		"cost      $2.00 total, $2.00 avg (1 sub(s))",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}

	// The sub histogram prints the whole vocabulary, in subResultOrder, zeros
	// included - one contiguous block, so this pins the render's ordering.
	subBlock := "merged    1\n  blocked   0\n  failed    0\n  conflict  0\n  skipped   1  (gate)\n  manual    0  (gate)\n"
	if !strings.Contains(out, subBlock) {
		t.Errorf("sub histogram block missing or misordered, want:\n%s\n---got---\n%s", subBlock, out)
	}

	// Per-kind statuses must follow runStatusOrder too. Re-render rather than
	// assert once: with only 3 statuses in the map, a single check would let an
	// unordered walk through ~1 run in 6.
	for i := 0; i < 30; i++ {
		got := renderJournalStats("app", "/tmp/j.jsonl", st)
		if !strings.Contains(got, "[1 done, 1 failed, 1 killed]") {
			t.Fatalf("per-kind statuses not in runStatusOrder (iteration %d)\n---\n%s", i, got)
		}
	}

	// "work" crashed with no end line -> no duration line for that kind. Scoped
	// by counting instead of matching an exact two-line string, so a future
	// padding tweak cannot make this pass vacuously.
	if n := strings.Count(out, "ended run(s)"); n != 1 {
		t.Errorf("expected exactly 1 per-kind duration line (only run-epic ended), got %d\n---\n%s", n, out)
	}
}

// A re-entrant epic re-records already-done/dep-blocked subs as "skipped" on
// every run, so the raw sub count drifts away from the work actually done. The
// header must separate the two.
func TestRenderJournalStatsSeparatesGateDecisions(t *testing.T) {
	st := aggregateJournal([]storage.JournalEntry{
		startEntry("run-epic", "app-9", 200),
		{
			Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-9", PID: 200,
			Status: "done", DurationS: 3600,
			Subs: []storage.JournalSub{
				{ID: "s1", Result: "merged", Turns: 10, CostUSD: 1},
				{ID: "s2", Result: "blocked", Turns: 5, CostUSD: 1},
				{ID: "s3", Result: "skipped"},
				{ID: "s4", Result: "skipped"},
				{ID: "s5", Result: "manual"},
			},
		},
	}, noneAlive, testNow)

	worked, gated := st.workerBacked()
	if worked != 2 || gated != 3 {
		t.Errorf("workerBacked() = (%d, %d), want (2, 3)", worked, gated)
	}
	out := renderJournalStats("app", "/tmp/j.jsonl", st)
	if !strings.Contains(out, "## Subs: 5  (2 worker-backed, 3 gate decision(s)") {
		t.Errorf("header must split worker-backed from gate decisions\n---\n%s", out)
	}
}

func TestRenderJournalStatsAlwaysPrintsTotals(t *testing.T) {
	// A journal with no subs at all (a killed run + a crash) must still print
	// the totals section - "0" is the answer, not a reason to omit it.
	st := aggregateJournal([]storage.JournalEntry{
		startEntry("run-epic", "app-5", 300),
		{Event: storage.JournalEventKilled, Kind: "run-epic", TaskID: "app-5", PID: 300},
		startEntry("work", "app-2", 400),
	}, noneAlive, testNow)
	out := renderJournalStats("app", "/tmp/j.jsonl", st)

	for _, want := range []string{
		"## Subs: 0",
		"## Totals",
		"duration  0s total, 0s avg (0 sub(s))",
		"turns     0 total, 0.0 avg (0 sub(s))",
		"cost      $0.00 total, $0.00 avg (0 sub(s))",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderJournalStatsShowsRunningRun(t *testing.T) {
	st := aggregateJournal([]storage.JournalEntry{startEntry("run-epic", "app-7", 700)},
		func(pid int) bool { return pid == 700 }, testNow)
	out := renderJournalStats("app", "/tmp/j.jsonl", st)

	if !strings.Contains(out, "running 1 (no end/killed line yet, pid still alive)") {
		t.Errorf("a live unpaired start should render as running\n---\n%s", out)
	}
	if strings.Contains(out, "crashed") {
		t.Errorf("a live run must not be reported as a crash\n---\n%s", out)
	}
}

func TestExecutorStatsCmdEmptyJournal(t *testing.T) {
	store, slug := tempStore(t)

	cmd := newExecutorStatsCmd(store)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{slug})

	// AC: empty/missing journal is a readable message, NOT an error.
	if err := cmd.Execute(); err != nil {
		t.Fatalf("missing journal must not error, got: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "no executor runs recorded yet") || !strings.Contains(got, slug) {
		t.Errorf("expected a readable empty-journal note, got %q", got)
	}
}

// The AC says "empty/missing" - a journal file that EXISTS but holds nothing
// usable is the other half, and takes a different code path (ReadJournal
// returns no error and no entries rather than short-circuiting on IsNotExist).
func TestExecutorStatsCmdEmptyAndCorruptJournalFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"existing but empty file", ""},
		{"only corrupt lines", "not json\n{\"broken\":\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, slug := tempStore(t)
			path := storage.JournalPath(store.ProjectDir(slug))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0644); err != nil {
				t.Fatalf("write journal: %v", err)
			}

			cmd := newExecutorStatsCmd(store)
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{slug})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("must not error, got: %v", err)
			}
			if got := buf.String(); !strings.Contains(got, "no executor runs recorded yet") {
				t.Errorf("expected the readable empty-journal note, got %q", got)
			}
		})
	}
}

func TestExecutorStatsCmdPrintsRollup(t *testing.T) {
	store, slug := tempStore(t)
	dir := store.ProjectDir(slug)
	entries := []storage.JournalEntry{
		startEntry("work", "proj-1", 100),
		{
			Event: storage.JournalEventEnd, Kind: "work", TaskID: "proj-1", PID: 100,
			Status: "done", DurationS: 300,
			Subs: []storage.JournalSub{{ID: "proj-1", Result: "merged", DurationS: 290, Turns: 20, CostUSD: 0.5}},
		},
	}
	for i := range entries {
		if err := storage.AppendJournal(dir, &entries[i]); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	cmd := newExecutorStatsCmd(store)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{slug})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"## Runs: 1", "work      1 run(s)  [1 done]", "## Subs: 1", "merged    1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestExecutorStatsCmdRegistered(t *testing.T) {
	store, _ := tempStore(t)
	for _, c := range newExecutorCmd(store).Commands() {
		if c.Name() == "stats" {
			return
		}
	}
	t.Fatal("`stats` is not registered under `pm executor`")
}
