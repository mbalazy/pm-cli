package storage

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// pinZone forces time.Local for the duration of a test. Without it every
// local-zone assertion below is a tautology on a UTC machine - which CI is -
// so the one property these helpers are built around (a stamp means the day
// its writer saw, not a UTC day) would be defended on no runner at all.
func pinZone(t *testing.T, loc *time.Location) {
	t.Helper()
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
}

func TestNow(t *testing.T) {
	pinZone(t, time.FixedZone("TEST", 5*60*60))

	got := Now()
	at, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("Now() = %q, not RFC3339: %v", got, err)
	}
	if d := time.Since(at); d < -time.Minute || d > time.Minute {
		t.Errorf("Now() = %q, %v away from the current instant", got, d)
	}
	// The local zone, not UTC: StampDate has to report the day the user saw.
	if _, offset := at.Zone(); offset != 5*60*60 {
		t.Errorf("Now() = %q, want the local zone offset %d", got, 5*60*60)
	}
}

func TestToday(t *testing.T) {
	if got, want := Today(), time.Now().Format("2006-01-02"); got != want {
		t.Errorf("Today() = %q, want %q", got, want)
	}
}

func TestParseStamp(t *testing.T) {
	// A zone that is neither UTC nor the dev machine's, so "bare date is local
	// midnight" is an assertion rather than a coincidence.
	pinZone(t, time.FixedZone("TEST", 5*60*60))

	tests := []struct {
		name string
		in   string
		ok   bool
		want time.Time
	}{
		{"rfc3339 with offset", "2026-09-02T16:34:40+02:00", true,
			time.Date(2026, 9, 2, 16, 34, 40, 0, time.FixedZone("", 2*60*60))},
		{"rfc3339 utc", "2026-09-02T14:34:40Z", true,
			time.Date(2026, 9, 2, 14, 34, 40, 0, time.UTC)},
		{"bare date is local midnight", "2026-09-02", true,
			time.Date(2026, 9, 2, 0, 0, 0, 0, time.FixedZone("TEST", 5*60*60))},
		{"empty", "", false, time.Time{}},
		{"garbage", "not-a-stamp", false, time.Time{}},
		{"date and time without a zone", "2026-09-02 16:34:40", false, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseStamp(tt.in)
			if ok != tt.ok {
				t.Fatalf("ParseStamp(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if !got.Equal(tt.want) {
				t.Errorf("ParseStamp(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestStampDate(t *testing.T) {
	// +02:00, the zone pm's own stamps are written in here.
	pinZone(t, time.FixedZone("TEST", 2*60*60))

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"rfc3339 keeps its own day", "2026-09-02T16:34:40+02:00", "2026-09-02"},
		// 00:30 local on the 2nd is still the 1st in UTC - the day a human saw
		// is the local one, which is why ParseStamp does not normalise to UTC.
		{"just after local midnight", "2026-09-02T00:30:00+02:00", "2026-09-02"},
		// A foreign offset is converted to the reader's day: 23:30 UTC is
		// already the 2nd here. Same frame relativeTime counts "today" in.
		{"foreign offset lands on the local day", "2026-09-01T23:30:00Z", "2026-09-02"},
		{"bare date passes through", "2026-09-02", "2026-09-02"},
		{"empty", "", ""},
		{"garbage is returned verbatim", "whenever", "whenever"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StampDate(tt.in); got != tt.want {
				t.Errorf("StampDate(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// An RFC3339 `updated` must survive a write/read round trip verbatim. yaml.v3
// resolves that shape to !!timestamp, so it quotes the value on the way out;
// the field stays a string, so a hand-written unquoted stamp reads back fine
// too. What this pins is that the round trip is byte-exact - a stamp turned
// into a time.Time and reformatted would silently change zone or precision.
func TestUpdatedStampSurvivesYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	stamp := "2026-09-02T16:34:40+02:00"
	task := &Task{
		Meta: TaskMeta{
			ID:      "a-1",
			Title:   "Stamped task",
			Status:  StatusTodo,
			Created: "2026-09-02",
			Updated: stamp,
		},
		FilePath: filepath.Join(dir, "a-1-stamped-task.md"),
		Body:     "body\n",
	}
	if err := writeTask(task); err != nil {
		t.Fatalf("writeTask: %v", err)
	}

	raw, err := os.ReadFile(task.FilePath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), `updated: "`+stamp+`"`) {
		t.Errorf("stamp not written as a quoted string:\n%s", raw)
	}

	got, err := ReadTask(task.FilePath)
	if err != nil {
		t.Fatalf("ReadTask: %v", err)
	}
	if got.Meta.Updated != stamp {
		t.Errorf("updated = %q, want %q", got.Meta.Updated, stamp)
	}
	if got.Meta.Created != "2026-09-02" {
		t.Errorf("created = %q, want %q", got.Meta.Created, "2026-09-02")
	}
}

// Task files are never migrated, so a real column holds both shapes at once.
// LessByOrder has to order the whole mixture - most recent first - not just
// the pairs of one shape.
func TestUpdatedStampsMixOldAndNew(t *testing.T) {
	stamps := []string{
		"2026-09-02T21:00:00+02:00", // today, evening
		"2026-09-02T09:00:00+02:00", // today, morning
		"2026-09-02",                // today, written before the switch
		"2026-09-01T23:00:00+02:00", // yesterday
		"2026-08-30",                // last week, before the switch
	}
	// Fed in reverse, so a comparator that never fires cannot pass.
	tasks := make([]*Task, 0, len(stamps))
	for i := len(stamps) - 1; i >= 0; i-- {
		if _, ok := ParseStamp(stamps[i]); !ok {
			t.Fatalf("ParseStamp(%q) failed", stamps[i])
		}
		// Same ID and order on every task, so LessByOrder falls through to the
		// stamp tie-break.
		tasks = append(tasks, &Task{Meta: TaskMeta{ID: "x-1", Updated: stamps[i]}})
	}
	sort.SliceStable(tasks, func(i, j int) bool { return LessByOrder(tasks[i], tasks[j]) })

	for i, want := range stamps {
		if tasks[i].Meta.Updated != want {
			t.Errorf("position %d = %q, want %q (full order: %v)", i, tasks[i].Meta.Updated, want, stampsOf(tasks))
			break
		}
	}
}

func stampsOf(tasks []*Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.Meta.Updated
	}
	return out
}
