package board

import (
	"fmt"
	"testing"
	"time"
)

// relativeTime is fed a task's `updated`, which comes in two shapes: an
// RFC3339 stamp since the timestamp switch, a bare date on every older file.
// The same calendar day must render the same way whichever shape carries it -
// counting elapsed hours instead of calendar days would make a task touched
// late yesterday read "today" while its legacy-stamped neighbour reads
// "1d ago".
func TestRelativeTime(t *testing.T) {
	now := time.Now()
	day := func(offsetDays int) time.Time {
		return now.AddDate(0, 0, -offsetDays)
	}
	// 23:00 local on the given day - the hour that separates calendar-day
	// counting from elapsed-hours counting for most of the working day.
	lateOn := func(offsetDays int) string {
		d := day(offsetDays)
		return time.Date(d.Year(), d.Month(), d.Day(), 23, 0, 0, 0, time.Local).Format(time.RFC3339)
	}
	dateOn := func(offsetDays int) string {
		return day(offsetDays).Format("2006-01-02")
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"today, bare date", dateOn(0), "today"},
		{"today, stamp at 00:05", func() string {
			d := day(0)
			return time.Date(d.Year(), d.Month(), d.Day(), 0, 5, 0, 0, time.Local).Format(time.RFC3339)
		}(), "today"},
		{"yesterday, bare date", dateOn(1), "1d ago"},
		{"yesterday, stamp at 23:00", lateOn(1), "1d ago"},
		{"5 days, bare date", dateOn(5), "5d ago"},
		{"5 days, stamp at 23:00", lateOn(5), "5d ago"},
		{"unparsable is returned verbatim", "whenever", "whenever"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relativeTime(tt.in); got != tt.want {
				t.Errorf("relativeTime(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The two shapes of one calendar day must never disagree, for any day in the
// recent past - including one straddling a DST switch.
func TestRelativeTimeAgreesAcrossStampShapes(t *testing.T) {
	now := time.Now()
	for i := 0; i < 400; i++ {
		d := now.AddDate(0, 0, -i)
		bare := d.Format("2006-01-02")
		stamp := time.Date(d.Year(), d.Month(), d.Day(), 23, 0, 0, 0, time.Local).Format(time.RFC3339)
		if a, b := relativeTime(bare), relativeTime(stamp); a != b {
			t.Fatalf("day -%d: bare %q -> %q, stamp %q -> %q", i, bare, a, stamp, b)
		}
		if i > 1 {
			if want := fmt.Sprintf("%dd ago", i); relativeTime(bare) != want {
				t.Fatalf("day -%d: got %q, want %q", i, relativeTime(bare), want)
			}
		}
	}
}
