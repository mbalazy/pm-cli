package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateJournalName(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"plain", "sim-rig", false},
		{"dots and underscores", "sim.rig_2", false},
		{"digit first", "2fa-sandbox", false},
		{"empty", "", true},
		{"uppercase", "SimRig", true},
		{"space", "sim rig", true},
		{"path separator", "sim/rig", true},
		{"parent dir", "..", true},
		{"escape", "../../etc/passwd", true},
		{"leading dot", ".hidden", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateJournalName(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateJournalName(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

// A journal name becomes a file name, so a name that escapes the project dir
// must be refused BEFORE anything is created - the same rule AddTask enforces
// for task IDs. Writing first and validating later would leave the file behind.
func TestAppendIncidentRefusesEscapingNameWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	err := AppendIncident(dir, "../escaped", &Incident{Symptom: "x"})
	if err == nil {
		t.Fatal("expected an error for an escaping journal name")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".journal")); !os.IsNotExist(statErr) {
		t.Fatalf("journal dir was created for a rejected name: %v", statErr)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dir)); len(entries) != 1 {
		t.Fatalf("something was written outside the project dir: %v", entries)
	}
}

func TestAppendIncidentRequiresSymptom(t *testing.T) {
	dir := t.TempDir()
	if err := AppendIncident(dir, "sim-rig", &Incident{Cause: "no symptom given"}); err == nil {
		t.Fatal("expected an error when symptom is empty")
	}
	if err := AppendIncident(dir, "sim-rig", &Incident{Symptom: "   "}); err == nil {
		t.Fatal("expected an error when symptom is blank")
	}
}

func TestAppendIncidentStampsTSAndAppends(t *testing.T) {
	dir := t.TempDir()
	first := &Incident{Symptom: "false RIG DEAD"}
	if err := AppendIncident(dir, "sim-rig", first); err != nil {
		t.Fatalf("append: %v", err)
	}
	if first.TS == "" {
		t.Fatal("TS was not stamped")
	}
	if err := AppendIncident(dir, "sim-rig", &Incident{
		Symptom: "stale bundle",
		TS:      "2026-07-30T10:00:00Z",
	}); err != nil {
		t.Fatalf("append second: %v", err)
	}

	got, err := ReadIncidents(dir, "sim-rig")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d incidents, want 2", len(got))
	}
	// Append-only: insertion order is preserved, NOT sorted by ts. The second
	// entry carries an older timestamp on purpose to prove nothing reorders.
	if got[0].Symptom != "false RIG DEAD" || got[1].Symptom != "stale bundle" {
		t.Fatalf("order not preserved: %+v", got)
	}
	if got[1].TS != "2026-07-30T10:00:00Z" {
		t.Fatalf("explicit TS was overwritten: %q", got[1].TS)
	}
}

func TestReadIncidentsMissingFileIsEmpty(t *testing.T) {
	got, err := ReadIncidents(t.TempDir(), "never-written")
	if err != nil {
		t.Fatalf("a declared-but-empty journal must not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d incidents, want 0", len(got))
	}
}

// One corrupt line must not hide the rest of the history - the file is the
// only record, and a half-written line is the likeliest way to damage it.
func TestReadIncidentsSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".journal"), 0755); err != nil {
		t.Fatal(err)
	}
	content := `{"ts":"2026-08-01T10:00:00Z","symptom":"first"}
{not json at all
{"ts":"2026-08-02T10:00:00Z"}
{"ts":"2026-08-03T10:00:00Z","symptom":"third"}
`
	if err := os.WriteFile(filepath.Join(dir, ".journal", "sim-rig.jsonl"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadIncidents(dir, "sim-rig")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d incidents, want 2 (corrupt + symptomless dropped): %+v", len(got), got)
	}
	if got[0].Symptom != "first" || got[1].Symptom != "third" {
		t.Fatalf("wrong survivors: %+v", got)
	}
}

func TestIncidentOpen(t *testing.T) {
	tests := []struct {
		name string
		fix  string
		want bool
	}{
		{"no fix is open", "", true},
		{"blank fix is open", "   ", true},
		{"fix closes it", "rig-check.sh resolves the sim via Metro", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Incident{Fix: tt.fix}).Open(); got != tt.want {
				t.Fatalf("Open() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAggregateIncidents(t *testing.T) {
	incidents := []Incident{
		{TS: "2026-07-30T10:00:00Z", Symptom: "silent instrumentation", Cause: "release build", CostMin: 40, Fix: "rig check", Tags: []string{"wrong-code"}},
		{TS: "2026-07-31T10:00:00Z", Symptom: "false pass", Cause: "persisted state", CostMin: 25, Tags: []string{"wrong-code", "state"}},
		{TS: "2026-08-03T10:00:00Z", Symptom: "false RIG DEAD", Cause: "wrong sim", CostMin: 20, Tags: []string{"wrong-sim"}},
		{TS: "not a timestamp", Symptom: "undated one", Tags: []string{"wrong-sim"}},
	}
	st := AggregateIncidents(incidents)

	if st.Total != 4 {
		t.Fatalf("Total = %d, want 4", st.Total)
	}
	if st.Open != 3 {
		t.Fatalf("Open = %d, want 3", st.Open)
	}
	if st.CostMin != 85 {
		t.Fatalf("CostMin = %d, want 85", st.CostMin)
	}
	// Costed is the average's denominator: entries with no cost recorded must
	// not drag the average down as if they had cost nothing.
	if st.Costed != 3 {
		t.Fatalf("Costed = %d, want 3", st.Costed)
	}
	if st.Undated != 1 {
		t.Fatalf("Undated = %d, want 1", st.Undated)
	}
	if st.First != "2026-07-30T10:00:00Z" || st.Last != "2026-08-03T10:00:00Z" {
		t.Fatalf("range = %s..%s", st.First, st.Last)
	}

	// Tags: count desc, then name. wrong-code(2) before wrong-sim(2)? No -
	// equal counts fall back to name, so wrong-code sorts first.
	wantTags := []TagCount{
		{Tag: "wrong-code", Count: 2, Open: 1},
		{Tag: "wrong-sim", Count: 2, Open: 2},
		{Tag: "state", Count: 1, Open: 1},
	}
	if len(st.Tags) != len(wantTags) {
		t.Fatalf("Tags = %+v, want %+v", st.Tags, wantTags)
	}
	for i, w := range wantTags {
		if st.Tags[i] != w {
			t.Fatalf("Tags[%d] = %+v, want %+v", i, st.Tags[i], w)
		}
	}

	wantMonths := []MonthCount{{Month: "2026-07", Count: 2}, {Month: "2026-08", Count: 1}}
	if len(st.Months) != len(wantMonths) {
		t.Fatalf("Months = %+v, want %+v", st.Months, wantMonths)
	}
	for i, w := range wantMonths {
		if st.Months[i] != w {
			t.Fatalf("Months[%d] = %+v, want %+v", i, st.Months[i], w)
		}
	}

	// Open list newest first, and the undated entry sorts LAST rather than
	// floating to the top on a raw string comparison.
	wantOpen := []string{"false RIG DEAD", "false pass", "undated one"}
	if len(st.OpenList) != len(wantOpen) {
		t.Fatalf("OpenList = %+v", st.OpenList)
	}
	for i, w := range wantOpen {
		if st.OpenList[i].Symptom != w {
			t.Fatalf("OpenList[%d] = %q, want %q (full: %+v)", i, st.OpenList[i].Symptom, w, st.OpenList)
		}
	}
}

// RFC3339 allows a numeric offset, so a purely lexicographic comparison gets
// the range backwards for mixed-zone entries. Both fields must come off the
// parsed instant.
func TestAggregateIncidentsRangeIsInstantNotString(t *testing.T) {
	st := AggregateIncidents([]Incident{
		{TS: "2026-08-03T09:00:00Z", Symptom: "later instant"},        // 09:00 UTC
		{TS: "2026-08-03T10:00:00+02:00", Symptom: "earlier instant"}, // 08:00 UTC
	})
	if st.First != "2026-08-03T10:00:00+02:00" {
		t.Fatalf("First = %q, want the +02:00 entry (08:00 UTC)", st.First)
	}
	if st.Last != "2026-08-03T09:00:00Z" {
		t.Fatalf("Last = %q, want the Z entry (09:00 UTC)", st.Last)
	}
}

func TestAggregateIncidentsEmpty(t *testing.T) {
	st := AggregateIncidents(nil)
	if st.Total != 0 || st.Open != 0 || len(st.Tags) != 0 || len(st.Months) != 0 || len(st.OpenList) != 0 {
		t.Fatalf("empty aggregate is not zero-valued: %+v", st)
	}
}

func TestJournalCounts(t *testing.T) {
	dir := t.TempDir()
	p := &Project{Journals: []JournalSubject{
		{Name: "sim-rig", Subject: "iOS simulator rig"},
		{Name: "never-used", Subject: "declared, never written"},
	}}
	if err := AppendIncident(dir, "sim-rig", &Incident{Symptom: "a", Fix: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := AppendIncident(dir, "sim-rig", &Incident{Symptom: "b"}); err != nil {
		t.Fatal(err)
	}

	got, err := JournalCounts(dir, p)
	if err != nil {
		t.Fatalf("JournalCounts: %v", err)
	}
	// Declaration order, and a declared-but-empty journal is REPORTED with
	// zeroes: "declared and empty" and "not declared" are different states.
	want := []JournalCount{
		{Name: "sim-rig", Subject: "iOS simulator rig", Total: 2, Open: 1},
		{Name: "never-used", Subject: "declared, never written", Total: 0, Open: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("counts[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestJournalCountsNoJournalsDeclared(t *testing.T) {
	got, err := JournalCounts(t.TempDir(), &Project{Name: "x"})
	if err != nil || got != nil {
		t.Fatalf("got %+v, %v - want nil, nil", got, err)
	}
	if got, err := JournalCounts(t.TempDir(), nil); err != nil || got != nil {
		t.Fatalf("nil project: got %+v, %v", got, err)
	}
}

func TestProjectJournalLookup(t *testing.T) {
	p := &Project{Journals: []JournalSubject{
		{Name: "sim-rig", Subject: "rig"},
		{Name: "flaky-e2e"},
	}}
	if j := p.Journal("sim-rig"); j == nil || j.Subject != "rig" {
		t.Fatalf("Journal(sim-rig) = %+v", j)
	}
	if j := p.Journal("nope"); j != nil {
		t.Fatalf("Journal(nope) = %+v, want nil", j)
	}
	if names := strings.Join(p.JournalNames(), ","); names != "sim-rig,flaky-e2e" {
		t.Fatalf("JournalNames = %q", names)
	}
	var nilProject *Project
	if nilProject.Journal("x") != nil || nilProject.JournalNames() != nil {
		t.Fatal("nil project must be safe")
	}
}

// project.yaml is hand-edited and merged on write; the journals list has to
// survive a round trip like every other known key.
func TestJournalsSurviveProjectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "project.yaml")
	p := &Project{
		Name:     "app",
		Journals: []JournalSubject{{Name: "sim-rig", Subject: "iOS simulator rig"}},
	}
	if err := WriteProject(path, p); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadProject(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Journals) != 1 || got.Journals[0].Name != "sim-rig" || got.Journals[0].Subject != "iOS simulator rig" {
		t.Fatalf("journals lost in round trip: %+v", got.Journals)
	}

	// And a re-write that touches an unrelated field keeps them.
	got.Notes = "changed"
	if err := WriteProject(path, got); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	again, err := ReadProject(path)
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if len(again.Journals) != 1 {
		t.Fatalf("journals dropped on rewrite: %+v", again.Journals)
	}
}
