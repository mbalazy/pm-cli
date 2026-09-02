package board

import (
	"fmt"
	"testing"
	"time"
)

// pinClock freezes the clock relativeTime reads, so an assertion about "which
// day is it" cannot fail on the one run that crosses midnight. It also pins
// the zone: the local one is what a stamp is interpreted in, so a UTC CI
// runner would otherwise never exercise the offset arithmetic at all.
func pinClock(t *testing.T, now time.Time) {
	t.Helper()
	prevNow, prevLoc := nowFn, time.Local
	nowFn = func() time.Time { return now }
	time.Local = now.Location()
	t.Cleanup(func() {
		nowFn, time.Local = prevNow, prevLoc
	})
}

// relativeTime is fed a task's `updated`, which comes in two shapes: an
// RFC3339 stamp since the timestamp switch, a bare date on every older file.
// The same calendar day must render the same way whichever shape carries it -
// counting elapsed hours instead of calendar days would make a task touched
// late yesterday read "today" while its legacy-stamped neighbour reads
// "1d ago".
func TestRelativeTime(t *testing.T) {
	warsaw := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, warsaw)
	pinClock(t, now)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"today, bare date", "2026-09-02", "today"},
		{"today, stamp at 00:05", "2026-09-02T00:05:00+02:00", "today"},
		{"today, stamp minutes ago", "2026-09-02T09:58:00+02:00", "today"},
		{"yesterday, bare date", "2026-09-01", "1d ago"},
		{"yesterday, stamp at 23:00", "2026-09-01T23:00:00+02:00", "1d ago"},
		{"5 days, bare date", "2026-08-28", "5d ago"},
		{"5 days, stamp at 23:00", "2026-08-28T23:00:00+02:00", "5d ago"},
		// A foreign-zone stamp is counted in the READER's day, the same frame
		// StampDate displays in: 23:30 UTC is already the 2nd at +02:00.
		{"utc stamp landing on the local today", "2026-09-01T23:30:00Z", "today"},
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

// The two shapes of one calendar day must never disagree, on any day of the
// past year - including the two straddling a DST switch, where a local day is
// 23 or 25 hours long.
func TestRelativeTimeAgreesAcrossStampShapes(t *testing.T) {
	warsaw, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skipf("no tzdata for Europe/Warsaw: %v", err)
	}
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, warsaw)
	pinClock(t, now)

	for i := 0; i < 400; i++ {
		d := now.AddDate(0, 0, -i)
		bare := d.Format("2006-01-02")
		// 23:00 is the hour that separates counting calendar days from
		// counting elapsed hours: at 10:00 the next morning it is 11 hours
		// old, which an hours/24 count reads as the same day.
		stamp := time.Date(d.Year(), d.Month(), d.Day(), 23, 0, 0, 0, warsaw).Format(time.RFC3339)
		a, b := relativeTime(bare), relativeTime(stamp)
		if a != b {
			t.Fatalf("day -%d: bare %q -> %q, stamp %q -> %q", i, bare, a, stamp, b)
		}
		want := "today"
		switch {
		case i == 1:
			want = "1d ago"
		case i > 1:
			want = fmt.Sprintf("%dd ago", i)
		}
		if a != want {
			t.Fatalf("day -%d (%s): got %q, want %q", i, bare, a, want)
		}
	}
}
