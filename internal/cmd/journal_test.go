package cmd

import (
	"strings"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

func simRig() *storage.JournalSubject {
	return &storage.JournalSubject{Name: "sim-rig", Subject: "iOS simulator rig"}
}

// An undeclared journal must NOT be created implicitly: a typo would open a
// second file and split a history in half without ever saying so.
func TestJournalSubjectRequiresDeclaration(t *testing.T) {
	proj := &storage.Project{Journals: []storage.JournalSubject{
		{Name: "sim-rig", Subject: "iOS simulator rig"},
		{Name: "flaky-e2e"},
	}}

	got, err := journalSubject(proj, "sim-rig")
	if err != nil || got == nil || got.Subject != "iOS simulator rig" {
		t.Fatalf("declared journal: got %+v, err %v", got, err)
	}

	_, err = journalSubject(proj, "sim-rgi")
	if err == nil {
		t.Fatal("expected an error for an undeclared journal")
	}
	// The error has to list what IS declared - a typo is the likeliest cause,
	// and the fix is one glance away only if the alternatives are on screen.
	for _, want := range []string{"sim-rgi", "sim-rig", "flaky-e2e"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

// With nothing declared at all the error must teach the config, not just deny.
func TestJournalSubjectNoneDeclaredShowsConfig(t *testing.T) {
	_, err := journalSubject(&storage.Project{Name: "x"}, "sim-rig")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"journals:", "name: sim-rig", "project.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestRenderJournalEntries(t *testing.T) {
	incidents := []storage.Incident{
		{TS: "2026-07-30T10:00:00Z", Symptom: "silent instrumentation", Cause: "release build", CostMin: 40, Fix: "rig check", Tags: []string{"wrong-code"}},
		{TS: "2026-08-03T10:00:00Z", Symptom: "false RIG DEAD", FalseConclusion: "the rig is dead", Cause: "wrong sim", CostMin: 20},
	}

	t.Run("newest first with open marker", func(t *testing.T) {
		out := renderJournalEntries("app", simRig(), incidents, 0, false)
		newest := strings.Index(out, "false RIG DEAD")
		oldest := strings.Index(out, "silent instrumentation")
		if newest < 0 || oldest < 0 || newest > oldest {
			t.Fatalf("expected newest first:\n%s", out)
		}
		if !strings.Contains(out, "fix:                 OPEN") {
			t.Fatalf("an entry with no fix must read OPEN:\n%s", out)
		}
		if !strings.Contains(out, "concluded (wrongly): the rig is dead") {
			t.Fatalf("false conclusion missing:\n%s", out)
		}
		if !strings.Contains(out, "2026-08-03") {
			t.Fatalf("date missing:\n%s", out)
		}
	})

	t.Run("open filter", func(t *testing.T) {
		out := renderJournalEntries("app", simRig(), incidents, 0, true)
		if strings.Contains(out, "silent instrumentation") {
			t.Fatalf("a fixed entry leaked into --open:\n%s", out)
		}
		if !strings.Contains(out, "false RIG DEAD") {
			t.Fatalf("the open entry is missing:\n%s", out)
		}
	})

	t.Run("limit says what it hid", func(t *testing.T) {
		out := renderJournalEntries("app", simRig(), incidents, 1, false)
		if strings.Contains(out, "silent instrumentation") {
			t.Fatalf("limit not applied:\n%s", out)
		}
		// Silent truncation would read as "that is the whole history".
		if !strings.Contains(out, "1 of 2 shown") {
			t.Fatalf("truncation not disclosed:\n%s", out)
		}
	})

	t.Run("empty", func(t *testing.T) {
		if out := renderJournalEntries("app", simRig(), nil, 0, false); !strings.Contains(out, "no entries yet") {
			t.Fatalf("empty journal: %q", out)
		}
		out := renderJournalEntries("app", simRig(), []storage.Incident{{TS: "2026-08-03T10:00:00Z", Symptom: "x", Fix: "done"}}, 0, true)
		if !strings.Contains(out, "no open entries") {
			t.Fatalf("all-closed journal under --open: %q", out)
		}
	})

	t.Run("undated entry renders without crashing", func(t *testing.T) {
		out := renderJournalEntries("app", simRig(), []storage.Incident{{TS: "", Symptom: "seeded by hand"}}, 0, false)
		if !strings.Contains(out, "seeded by hand") {
			t.Fatalf("undated entry dropped:\n%s", out)
		}
	})
}

func TestRenderIncidentStats(t *testing.T) {
	st := storage.AggregateIncidents([]storage.Incident{
		{TS: "2026-07-30T10:00:00Z", Symptom: "silent instrumentation", CostMin: 40, Fix: "rig check", Tags: []string{"wrong-code"}},
		{TS: "2026-07-31T10:00:00Z", Symptom: "false pass", Cause: "persisted state", CostMin: 25, Tags: []string{"wrong-code"}},
		{TS: "2026-08-03T10:00:00Z", Symptom: "false RIG DEAD", Cause: "wrong sim", CostMin: 20, Tags: []string{"wrong-sim"}},
	})
	out := renderIncidentStats("app", simRig(), st)

	for _, want := range []string{
		"entries: 3",
		"open:    2",
		"1h25",             // 85 minutes, read as time rather than as a bare number
		"avg 28 min",       // 85/3 - denominator is entries that RECORDED a cost
		"2026-07",          // month histogram
		"wrong-code",       // tag cluster, most frequent first
		"Open - no fix",    // the derived backlog
		"cause: wrong sim", // open entries carry their cause
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats output missing %q:\n%s", want, out)
		}
	}

	// The most frequent tag must come first - it is the one arguing for a fix.
	if strings.Index(out, "wrong-code") > strings.Index(out, "wrong-sim") {
		t.Fatalf("tags not ordered by count:\n%s", out)
	}
}

// The cost average must divide by entries that recorded a cost. Dividing by
// the total would make a subsystem look CHEAPER the more often the field was
// skipped - the opposite of what the number is for.
func TestRenderIncidentStatsAverageIgnoresUncostedEntries(t *testing.T) {
	st := storage.AggregateIncidents([]storage.Incident{
		{TS: "2026-08-01T10:00:00Z", Symptom: "a", CostMin: 60},
		{TS: "2026-08-02T10:00:00Z", Symptom: "b"},
		{TS: "2026-08-03T10:00:00Z", Symptom: "c"},
	})
	out := renderIncidentStats("app", simRig(), st)
	if !strings.Contains(out, "avg 60 min") {
		t.Fatalf("average must be 60 (one costed entry), got:\n%s", out)
	}
	if !strings.Contains(out, "1 entry that recorded one") {
		t.Fatalf("the denominator must be stated:\n%s", out)
	}
}

func TestRenderIncidentStatsEmpty(t *testing.T) {
	out := renderIncidentStats("app", simRig(), storage.AggregateIncidents(nil))
	if !strings.Contains(out, "no entries yet") {
		t.Fatalf("empty stats: %q", out)
	}
}

func TestHumanMinutes(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0 min"},
		{45, "45 min"},
		{60, "1h00"},
		{85, "1h25"},
		{600, "10h00"},
	}
	for _, tt := range tests {
		if got := humanMinutes(tt.in); got != tt.want {
			t.Fatalf("humanMinutes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
