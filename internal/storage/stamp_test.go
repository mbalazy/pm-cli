package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNow(t *testing.T) {
	got := Now()
	at, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("Now() = %q, not RFC3339: %v", got, err)
	}
	if d := time.Since(at); d < -time.Minute || d > time.Minute {
		t.Errorf("Now() = %q, %v away from the current instant", got, d)
	}
	// The local zone, not UTC: StampDate has to report the day the user saw.
	if _, offset := at.Zone(); offset != nowLocalOffset() {
		t.Errorf("Now() = %q, want the local zone offset %d", got, nowLocalOffset())
	}
}

func nowLocalOffset() int {
	_, offset := time.Now().Zone()
	return offset
}

func TestToday(t *testing.T) {
	if got, want := Today(), time.Now().Format("2006-01-02"); got != want {
		t.Errorf("Today() = %q, want %q", got, want)
	}
}

func TestParseStamp(t *testing.T) {
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
			time.Date(2026, 9, 2, 0, 0, 0, 0, time.Local)},
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
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"rfc3339 keeps its own day", "2026-09-02T16:34:40+02:00", "2026-09-02"},
		// 00:30 local on the 2nd is still the 1st in UTC - the day a human saw
		// is the local one, which is why ParseStamp does not normalise to UTC.
		{"just after local midnight", "2026-09-02T00:30:00+02:00", "2026-09-02"},
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

// An RFC3339 `updated` must survive a write/read round trip as a STRING. Left
// unquoted, yaml.v3 resolves that shape to !!timestamp, and the whole task
// file would stop parsing - the failure mode the field type guards against.
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

// A task file written before the switch holds a bare date. Both shapes have to
// coexist unmigrated, and a same-day pair has to order by time.
func TestUpdatedStampsMixOldAndNew(t *testing.T) {
	old := "2026-09-02"
	morning := "2026-09-02T09:00:00+02:00"
	evening := "2026-09-02T21:00:00+02:00"

	for _, s := range []string{old, morning, evening} {
		if _, ok := ParseStamp(s); !ok {
			t.Fatalf("ParseStamp(%q) failed", s)
		}
	}
	// The string ordering LessByOrder and the board columns rely on.
	if !(old < morning && morning < evening) {
		t.Errorf("string order broken: %q %q %q", old, morning, evening)
	}
}
