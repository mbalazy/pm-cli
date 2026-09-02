package storage

import "time"

// stampDateLayout is the date-only stamp format: what `created` has always
// been, and what `updated` was before it moved to a full timestamp.
const stampDateLayout = "2006-01-02"

// Today returns the current date as YYYY-MM-DD. It stamps `created`, which
// never needs sub-day resolution, and is what focus plans compare a plan's
// date against.
func Today() string {
	return time.Now().Format(stampDateLayout)
}

// Now returns the current local time as an RFC3339 timestamp. It stamps
// `updated`, where ordering WITHIN a day is what makes "most recent first"
// mean anything - a bare date leaves every task touched today tied - and
// `status_changed`, where the same resolution is what makes "waiting since
// this morning" distinguishable from "waiting since last night".
func Now() string {
	return time.Now().Format(time.RFC3339)
}

// ParseStamp parses the stamp formats pm writes: RFC3339 for anything
// machine-written, a bare date for `created` and for any `updated` written
// before the timestamp switch. Task files are never migrated, so both shapes
// stay readable forever.
//
// The date is parsed IN THE LOCAL ZONE, because that is where it was written
// (Today formats time.Now()). Reading it as UTC midnight would shift a whole
// day's tasks east of UTC by the offset, so a run that finished at 01:30
// local - stamped the previous day in UTC - would sort below a tracker whose
// task file was merely touched today.
func ParseStamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if at, err := time.Parse(time.RFC3339, s); err == nil {
		return at, true
	}
	if at, err := time.ParseInLocation(stampDateLayout, s, time.Local); err == nil {
		return at, true
	}
	return time.Time{}, false
}

// StampDate returns the YYYY-MM-DD part of a stamp, for human display where a
// full timestamp would only make a card or a header wider. An unparsable
// stamp is returned verbatim - showing what the file holds beats showing
// nothing.
//
// The day is the READER's, not the writer's: a stamp carrying a foreign offset
// is converted to the local zone first. Every display of `updated` has to
// answer "which day is this" the same way - a header reading "2026-09-02" next
// to a list reading "today" would just be two frames on one screen - and
// "today" can only ever mean the reader's today. For the stamps pm actually
// writes (Now uses the local zone) the conversion is a no-op.
func StampDate(s string) string {
	if at, ok := ParseStamp(s); ok {
		return at.In(time.Local).Format(stampDateLayout)
	}
	return s
}
