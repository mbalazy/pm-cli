package cmd

import (
	"bytes"
	"strings"
	"testing"

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

func TestAggregateJournal(t *testing.T) {
	tests := []struct {
		name    string
		entries []storage.JournalEntry
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
			name: "same task re-run - PID discriminates, one crashes",
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
			name: "missing status and result degrade to unknown",
			entries: []storage.JournalEntry{
				startEntry("work", "app-4", 600),
				{
					Event: storage.JournalEventEnd, Kind: "work", TaskID: "app-4", PID: 600,
					Subs: []storage.JournalSub{{ID: "app-4"}},
				},
			},
			want: journalStats{
				Runs:       1,
				Kinds:      map[string]*kindStats{"work": {Runs: 1, Statuses: map[string]int{runStatusUnknown: 1}}},
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

			got := aggregateJournal(entries)

			if got.Entries != len(tt.entries) {
				t.Errorf("Entries = %d, want %d", got.Entries, len(tt.entries))
			}
			if got.Runs != tt.want.Runs {
				t.Errorf("Runs = %d, want %d", got.Runs, tt.want.Runs)
			}
			if got.Crashes != tt.want.Crashes {
				t.Errorf("Crashes = %d, want %d", got.Crashes, tt.want.Crashes)
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
		got := orderedKeys(counts, subResultOrder)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("orderedKeys = %v, want %v", got, want)
		}
	}
	if got := orderedKeys(map[string]int{"merged": 0}, subResultOrder); len(got) != 0 {
		t.Errorf("zero counts should be omitted, got %v", got)
	}
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
		startEntry("work", "app-2", 400), // crash
	})
	out := renderJournalStats("app", "/tmp/app/.executor/journal.jsonl", st)

	for _, want := range []string{
		"# executor stats: app",
		"/tmp/app/.executor/journal.jsonl (3 entries)",
		"## Runs: 2",
		"run-epic  1 run(s)  [1 done]",
		"crashed:  1",
		"## Subs: 2",
		"merged    1",
		"skipped   1",
		"turns     60 total, 60.0 avg (1 sub(s))",
		"cost      $2.00 total, $2.00 avg (1 sub(s))",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
	// "work" crashed with no end line -> no duration line for that kind
	if strings.Contains(out, "work      1 run(s)  [1 crashed]\n            duration") {
		t.Errorf("crash-only kind should print no duration line\n---\n%s", out)
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
