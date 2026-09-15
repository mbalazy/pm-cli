package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendTimelineEntryAcrossMonths(t *testing.T) {
	dir := t.TempDir()
	sep := &TimelineEntry{Kind: TimelineEvent, Text: "client moved the deadline", TS: "2026-09-02T10:00:00+02:00", Refs: []string{"proj-3", " "}}
	aug := &TimelineEntry{Kind: TimelineDecision, Text: "one product on two rails", TS: "2026-08-20T09:00:00+02:00"}
	for _, e := range []*TimelineEntry{sep, aug} {
		if err := AppendTimelineEntry(dir, e); err != nil {
			t.Fatalf("append: %v", err)
		}
		if e.ID == "" {
			t.Fatalf("id not stamped: %+v", e)
		}
	}

	for month, want := range map[string]*TimelineEntry{"2026-08": aug, "2026-09": sep} {
		data, err := os.ReadFile(TimelineFilePath(dir, month))
		if err != nil {
			t.Fatalf("month file %s: %v", month, err)
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) != 1 {
			t.Fatalf("%s: want one line, got %d:\n%s", month, len(lines), data)
		}
		var got TimelineEntry
		if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
			t.Fatalf("%s: not JSON: %v", month, err)
		}
		if got.ID != want.ID || got.TS != want.TS || got.Text != want.Text {
			t.Fatalf("%s: got %+v, want %+v", month, got, want)
		}
	}
	if len(sep.Refs) != 1 || sep.Refs[0] != "proj-3" {
		t.Fatalf("blank refs not dropped: %v", sep.Refs)
	}

	got, err := ReadTimeline(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Written September first, read August first: the read is chronological.
	if len(got) != 2 || got[0].Text != aug.Text || got[1].Text != sep.Text {
		t.Fatalf("read order: %+v", got)
	}
}

func TestAppendTimelineEntryStampsTS(t *testing.T) {
	dir := t.TempDir()
	e := &TimelineEntry{Kind: TimelineState, Text: "  where we stand\n"}
	if err := AppendTimelineEntry(dir, e); err != nil {
		t.Fatalf("append: %v", err)
	}
	when, ok := e.When()
	if !ok || time.Since(when) > time.Minute {
		t.Fatalf("ts not stamped to now: %q", e.TS)
	}
	if e.Text != "where we stand" {
		t.Fatalf("text not trimmed: %q", e.Text)
	}
	if _, err := os.Stat(TimelineFilePath(dir, when.Format("2006-01"))); err != nil {
		t.Fatalf("month file of the stamped ts: %v", err)
	}
}

func TestAppendTimelineEntryRejectsWithoutWriting(t *testing.T) {
	tests := []struct {
		name  string
		entry TimelineEntry
	}{
		{"unknown kind", TimelineEntry{Kind: "note", Text: "x"}},
		{"empty kind", TimelineEntry{Text: "x"}},
		{"empty text", TimelineEntry{Kind: TimelineEvent, Text: ""}},
		{"blank text", TimelineEntry{Kind: TimelineEvent, Text: " \n\t"}},
		{"unparseable ts", TimelineEntry{Kind: TimelineEvent, Text: "x", TS: "2026-09-15"}},
		{"dated tomorrow", TimelineEntry{Kind: TimelineState, Text: "x", TS: tomorrowMidnight()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			e := tt.entry
			if err := AppendTimelineEntry(dir, &e); err == nil {
				t.Fatal("expected an error")
			}
			if _, err := os.Stat(TimelineDir(dir)); !os.IsNotExist(err) {
				t.Fatalf("timeline dir created for a rejected entry: %v", err)
			}
		})
	}
}

func tomorrowMidnight() string {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()).Format(time.RFC3339)
}

// Today stays writable to its last second, so a back-date to today's
// midnight and a stamp of "now" both pass.
func TestAppendTimelineEntryAcceptsToday(t *testing.T) {
	now := time.Now()
	for _, ts := range []string{
		time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Format(time.RFC3339),
		now.Format(time.RFC3339),
	} {
		if err := AppendTimelineEntry(t.TempDir(), &TimelineEntry{Kind: TimelineEvent, Text: "x", TS: ts}); err != nil {
			t.Fatalf("ts %s: %v", ts, err)
		}
	}
}

func TestTimelineEntryIDStable(t *testing.T) {
	a := timelineEntryID("2026-09-15T10:00:00+02:00", TimelineEvent, "x")
	if a != timelineEntryID("2026-09-15T10:00:00+02:00", TimelineEvent, "x") {
		t.Fatal("same inputs, different id")
	}
	if !strings.HasPrefix(a, "20260915-") {
		t.Fatalf("id does not lead with the entry's day: %q", a)
	}
	// A back-dated entry at local midnight east of UTC is still the 1st in
	// its id, not the 31st the UTC clock would say.
	if b := timelineEntryID("2026-08-01T00:00:00+02:00", TimelineEvent, "x"); !strings.HasPrefix(b, "20260801-") {
		t.Fatalf("id does not lead with the entry's own day: %q", b)
	}
	if a == timelineEntryID("2026-09-15T10:00:00+02:00", TimelineDecision, "x") {
		t.Fatal("kind does not change the id")
	}

	// A line written by hand without an id reads back with the id a write
	// would have stamped.
	dir := t.TempDir()
	written := &TimelineEntry{Kind: TimelineEvent, Text: "x", TS: "2026-09-15T10:00:00+02:00"}
	if err := AppendTimelineEntry(dir, written); err != nil {
		t.Fatal(err)
	}
	line := `{"ts":"2026-09-16T10:00:00+02:00","kind":"event","text":"by hand"}` + "\n"
	appendRaw(t, dir, "2026-09", line)
	got, err := ReadTimeline(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != written.ID || got[1].ID != timelineEntryID("2026-09-16T10:00:00+02:00", TimelineEvent, "by hand") {
		t.Fatalf("ids on read: %+v", got)
	}
}

func appendRaw(t *testing.T, dir, month, content string) {
	t.Helper()
	if err := os.MkdirAll(TimelineDir(dir), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(TimelineFilePath(dir, month), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestReadTimelineSkipsBadLinesAndSortsUndatedLast(t *testing.T) {
	dir := t.TempDir()
	appendRaw(t, dir, "2026-09",
		`{"ts":"not a timestamp","kind":"event","text":"seeded by hand"}`+"\n"+
			`not json`+"\n"+
			`{"ts":"2026-09-10T10:00:00Z","kind":"note","text":"unknown kind"}`+"\n"+
			`{"ts":"2026-09-10T10:00:00Z","kind":"event","text":""}`+"\n"+
			`{"ts":"2026-09-11T10:00:00Z","kind":"event","text":"later"}`+"\n"+
			// An offset makes this instant EARLIER than the line above, though
			// its string compares greater.
			`{"ts":"2026-09-11T11:00:00+02:00","kind":"event","text":"earlier"}`+"\n")
	got, err := ReadTimeline(dir)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range got {
		texts = append(texts, e.Text)
	}
	if strings.Join(texts, "|") != "earlier|later|seeded by hand" {
		t.Fatalf("read = %v", texts)
	}
	newest := FilterTimeline(got, time.Time{}, "")
	if newest[0].Text != "later" || newest[2].Text != "seeded by hand" {
		t.Fatalf("newest first, undated last: %+v", newest)
	}
}

// Whatever the write side accepts, the read side must read back: a long state
// must not lock the whole timeline behind a line-length limit.
func TestReadTimelineLongLine(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("<", 400_000) // JSON-escaped to 2.4 MB on disk
	if err := AppendTimelineEntry(dir, &TimelineEntry{Kind: TimelineState, Text: long, TS: day(1)}); err != nil {
		t.Fatal(err)
	}
	if err := AppendTimelineEntry(dir, &TimelineEntry{Kind: TimelineEvent, Text: "after", TS: day(2)}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTimeline(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || got[0].Text != long || got[1].Text != "after" {
		t.Fatalf("read %d entries", len(got))
	}
}

func TestReadTimelineMissingDir(t *testing.T) {
	dir := t.TempDir()
	r, err := ReadTimelineDefault(dir, time.Now())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if r.State != nil || len(r.Since) != 0 || r.Total != 0 || r.Stale {
		t.Fatalf("empty read = %+v", r)
	}
	if r.Since == nil {
		t.Fatal("since must be an empty list, not null")
	}
	if _, err := os.Stat(TimelineDir(dir)); !os.IsNotExist(err) {
		t.Fatalf("a read created the timeline dir: %v", err)
	}
}

// day returns 2026-09-<d> 10:00 UTC as RFC3339.
func day(d int) string {
	return time.Date(2026, 9, d, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
}

func TestTimelineDefaultRead(t *testing.T) {
	entries := []TimelineEntry{
		{TS: day(1), Kind: TimelineState, Text: "S1"},
		{TS: day(2), Kind: TimelineEvent, Text: "E1"},
		{TS: day(3), Kind: TimelineDecision, Text: "E2"},
		{TS: day(5), Kind: TimelineState, Text: "S2"},
		{TS: day(6), Kind: TimelineEvent, Text: "E3"},
		{TS: day(6), Kind: TimelineEvent, Text: "E4"},
		{TS: day(7), Kind: TimelineDecision, Text: "E5"},
	}
	r := TimelineDefaultRead(entries, time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC))
	if r.State == nil || r.State.Text != "S2" {
		t.Fatalf("state = %+v", r.State)
	}
	var since []string
	for _, e := range r.Since {
		since = append(since, e.Text)
	}
	if strings.Join(since, ",") != "E3,E4,E5" {
		t.Fatalf("since = %v", since)
	}
	if r.EntriesSince != 3 || r.DaysSince != 3 || r.Stale || r.Total != 7 || r.Note != "" {
		t.Fatalf("read = %+v", r)
	}

	// The entries before the latest state are out of the default read but
	// still in the timeline.
	all := FilterTimeline(entries, time.Time{}, "")
	for _, want := range []string{"S1", "E1", "E2"} {
		found := false
		for _, e := range all {
			found = found || e.Text == want
		}
		if !found {
			t.Fatalf("%s missing from the full list", want)
		}
	}
}

func TestTimelineDefaultReadStale(t *testing.T) {
	state := TimelineEntry{TS: day(1), Kind: TimelineState, Text: "S"}
	withEvents := func(n int) []TimelineEntry {
		out := []TimelineEntry{state}
		for i := range n {
			out = append(out, TimelineEntry{TS: day(1)[:11] + fmt.Sprintf("11:%02d:00Z", i), Kind: TimelineEvent, Text: fmt.Sprintf("E%d", i)})
		}
		return out
	}
	at := func(d int) time.Time { return time.Date(2026, 9, 1+d, 10, 0, 0, 0, time.UTC) }

	tests := []struct {
		name      string
		entries   []TimelineEntry
		now       time.Time
		wantStale bool
		wantCount int
		wantDays  int
	}{
		{"10 entries", withEvents(10), at(1), true, 10, 1},
		{"9 entries", withEvents(9), at(1), false, 9, 1},
		{"7 days", withEvents(0), at(7), true, 0, 7},
		{"6 days", withEvents(0), at(6), false, 0, 6},
		{"6 days 23 hours", withEvents(0), at(7).Add(-time.Hour), false, 0, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := TimelineDefaultRead(tt.entries, tt.now)
			if r.Stale != tt.wantStale || r.EntriesSince != tt.wantCount || r.DaysSince != tt.wantDays {
				t.Fatalf("read = stale %v, entries %d, days %d", r.Stale, r.EntriesSince, r.DaysSince)
			}
			if tt.wantStale && !strings.HasPrefix(r.Note, "stale: ") {
				t.Fatalf("stale note = %q", r.Note)
			}
			if !tt.wantStale && r.Note != "" {
				t.Fatalf("note on a fresh state: %q", r.Note)
			}
		})
	}
}

func TestTimelineDefaultReadNoState(t *testing.T) {
	var entries []TimelineEntry
	for i := 1; i <= 12; i++ {
		entries = append(entries, TimelineEntry{TS: day(i), Kind: TimelineEvent, Text: fmt.Sprintf("E%d", i)})
	}
	r := TimelineDefaultRead(entries, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if r.State != nil || r.Stale || r.Total != 12 {
		t.Fatalf("read = %+v", r)
	}
	if len(r.Since) != TimelineNoStateLimit || r.Since[0].Text != "E3" || r.Since[9].Text != "E12" {
		t.Fatalf("since = %+v", r.Since)
	}
	if !strings.HasPrefix(r.Note, "no state yet") || !strings.Contains(r.Note, "10 of 12") {
		t.Fatalf("note = %q", r.Note)
	}
}

// A hand-edited state with a broken ts must not hide the real latest state.
func TestTimelineDefaultReadPrefersDatedState(t *testing.T) {
	entries := []TimelineEntry{
		{TS: day(1), Kind: TimelineState, Text: "real"},
		{TS: day(2), Kind: TimelineEvent, Text: "E1"},
		{TS: "garbage", Kind: TimelineState, Text: "hand-edited"},
	}
	r := TimelineDefaultRead(entries, time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC))
	if r.State == nil || r.State.Text != "real" || len(r.Since) != 2 {
		t.Fatalf("read = %+v", r)
	}
}

func TestFilterTimeline(t *testing.T) {
	entries := []TimelineEntry{
		{TS: day(1), Kind: TimelineState, Text: "S1"},
		{TS: day(2), Kind: TimelineEvent, Text: "E1"},
		{TS: day(3), Kind: TimelineDecision, Text: "D1"},
		{TS: day(4), Kind: TimelineEvent, Text: "E2"},
		{TS: "", Kind: TimelineEvent, Text: "undated"},
	}
	texts := func(es []TimelineEntry) string {
		var out []string
		for _, e := range es {
			out = append(out, e.Text)
		}
		return strings.Join(out, ",")
	}
	since := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		since time.Time
		kind  string
		want  string
	}{
		{"all", time.Time{}, "", "E2,D1,E1,S1,undated"},
		{"since", since, "", "E2,D1,E1"},
		{"kind", time.Time{}, TimelineEvent, "E2,E1,undated"},
		{"since and kind", since, TimelineEvent, "E2,E1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := texts(FilterTimeline(entries, tt.since, tt.kind)); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
	if got := FilterTimeline(nil, time.Time{}, ""); got == nil {
		t.Fatal("an empty filter result must be a list, not null")
	}
}

func TestTimelineFilesLiveUnderProjectDir(t *testing.T) {
	if got := TimelineFilePath("/p", "2026-09"); got != filepath.Join("/p", ".timeline", "2026-09.jsonl") {
		t.Fatalf("path = %s", got)
	}
}
