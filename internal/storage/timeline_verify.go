package storage

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Provenance markers on state lines.
//
// A state is prose written by a model, and a model has no internal flag that
// separates "I read this in a tool result" from "I assume this". A state line
// written from an inference is read by the next session as a fact, that
// session writes it into its own state, and the loop closes: the claim now
// has a paper trail and no evidence. The 2026-09-11..15 webapp session did
// exactly this with "SENTRY_DSN not set on the deploys" - born from an absence
// of errors, carried through a brief into a PR description as a "known gap",
// against spans the compaction had dropped.
//
// The marker is the knowledge base's `verified`/`verify` pair folded into the
// line itself, because a state is plain text (`--text -`) and a per-line
// structured field would need line indexes that break on the first edit:
//
//	<claim> [verified 2026-09-14 by gh api repos/x/deployments]
//	<claim> [assumed]
//	<claim> [assumed - no access to the prod console]
//
// A verified line names the DATE and the SOURCE (a command, a document); an
// assumed line says so. A line with neither is UNMARKED, and the read never
// counts it as verified. A verified line older than TimelineRecheckDays is
// due for a re-check: the read lists those lines by number, which is what
// the post-compaction hook prints and what a session acts on. The marker is
// parsed on read only; the stored text is untouched, so every state written
// before the marker existed still reads (as all-unmarked).

// Marks a state line can carry.
const (
	VerifyVerified = "verified"
	VerifyAssumed  = "assumed"
)

// TimelineRecheckDays is the age at which a verified line is due for a
// re-check. The same number as TimelineStaleDays on purpose: one figure to
// remember, and a fact about external state drifts about as fast as a state.
const TimelineRecheckDays = 7

// A marker is a bracketed keyword at the END of the line; anything else in
// brackets is text. The keyword is matched case-insensitively.
var stateMarkerRe = regexp.MustCompile(`(?i)\s*\[(verified|assumed)\b([^\]]*)\]\s*$`)

var isoDateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// StateLine is one non-blank line of a state's text with its marker parsed.
type StateLine struct {
	// N is the 1-based line number in the state's text, blank lines counted,
	// so a reader can point at the line in the raw text.
	N int `json:"line"`
	// Text is the line without its marker.
	Text string `json:"text"`
	// Mark is VerifyVerified, VerifyAssumed or "" for an unmarked line.
	Mark string `json:"mark,omitempty"`
	// Date is the verification day (YYYY-MM-DD) of a verified line.
	Date string `json:"date,omitempty"`
	// Source is what verified the line - a command, a document. May be empty.
	Source string `json:"source,omitempty"`
	// Days is whole days from Date to the read; verified lines only.
	Days int `json:"days,omitempty"`
	// Recheck is set on a verified line Days >= TimelineRecheckDays old.
	Recheck bool `json:"recheck,omitempty"`
}

// ParseStateLines splits a state's text into its non-blank lines and parses
// the marker of each. Pure: no clock, so Days/Recheck stay unset here.
func ParseStateLines(text string) []StateLine {
	var out []StateLine
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		sl := StateLine{N: i + 1, Text: line}
		if m := stateMarkerRe.FindStringSubmatchIndex(line); m != nil {
			sl.Text = strings.TrimSpace(line[:m[0]])
			sl.Mark = strings.ToLower(line[m[2]:m[3]])
			rest := strings.TrimSpace(line[m[4]:m[5]])
			if sl.Mark == VerifyVerified {
				if d := isoDateRe.FindStringIndex(rest); d != nil {
					sl.Date = rest[d[0]:d[1]]
					src := strings.TrimSpace(rest[d[1]:])
					src = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(src, "by:"), "by"))
					sl.Source = strings.TrimSpace(strings.TrimPrefix(src, ":"))
				}
			}
		}
		out = append(out, sl)
	}
	return out
}

// ValidateStateMarkers refuses a state whose `[verified ...]` marker carries
// no YYYY-MM-DD date or a date after today: stored as is, such a line would
// READ as verified in the raw text while the parser counts it unmarked, which
// is the silent case the marker exists to remove. Called on write only, so a
// hand-edited line still reads. Non-state entries always pass.
func ValidateStateMarkers(kind, text string, now time.Time) error {
	if kind != TimelineState {
		return nil
	}
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	for _, sl := range ParseStateLines(text) {
		if sl.Mark != VerifyVerified {
			continue
		}
		if sl.Date == "" {
			return invalidValue("state line %d: a [verified ...] marker needs the day it was checked, e.g. [verified %s by <command or doc>]", sl.N, now.Format("2006-01-02"))
		}
		d, err := time.ParseInLocation("2006-01-02", sl.Date, now.Location())
		if err != nil {
			return invalidValue("state line %d: [verified %s] is not a calendar day (want YYYY-MM-DD)", sl.N, sl.Date)
		}
		if !d.Before(tomorrow) {
			return invalidValue("state line %d: [verified %s] is in the future - a verification already happened", sl.N, sl.Date)
		}
	}
	return nil
}

// TimelineVerification is the provenance picture of one state at read time.
type TimelineVerification struct {
	// Verified counts lines verified within TimelineRecheckDays.
	Verified int `json:"verified"`
	// Recheck counts verified lines TimelineRecheckDays or more days old.
	Recheck int `json:"recheck"`
	// Assumed counts lines marked assumed.
	Assumed int `json:"assumed"`
	// Unmarked counts lines with no marker - never read as verified.
	Unmarked int `json:"unmarked"`
	// RecheckLines lists the lines due for a re-check, in text order.
	RecheckLines []StateLine `json:"recheck_lines"`
	// Note says what to do when there is something to do: re-check the
	// listed lines, or mark a state that carries no markers at all.
	Note string `json:"note,omitempty"`
}

// Marked reports whether the state carries any marker at all. A state with
// none renders as before the markers existed; a state with some gets a
// gutter that shows which lines are due or assumed.
func (v TimelineVerification) Marked() bool {
	return v.Verified+v.Recheck+v.Assumed > 0
}

// VerifyState classifies every line of a state's text against now.
func VerifyState(text string, now time.Time) TimelineVerification {
	v := TimelineVerification{RecheckLines: []StateLine{}}
	for _, sl := range ParseStateLines(text) {
		switch sl.Mark {
		case VerifyAssumed:
			v.Assumed++
		case VerifyVerified:
			// A verified line with no readable day (a hand edit; the write
			// side refuses it) is due at once - "verified" with no date is
			// exactly the claim the marker exists to flag.
			d, err := time.ParseInLocation("2006-01-02", sl.Date, now.Location())
			dated := err == nil
			if dated && now.After(d) {
				sl.Days = int(now.Sub(d).Hours() / 24)
			}
			if !dated || sl.Days >= TimelineRecheckDays {
				sl.Recheck = true
				v.Recheck++
				v.RecheckLines = append(v.RecheckLines, sl)
			} else {
				v.Verified++
			}
		default:
			v.Unmarked++
		}
	}
	switch {
	case v.Recheck > 0:
		v.Note = fmt.Sprintf("%d line%s verified %d+ days ago - re-check %s before relying on %s",
			v.Recheck, plural(v.Recheck), TimelineRecheckDays,
			map[bool]string{true: "it", false: "them"}[v.Recheck == 1],
			map[bool]string{true: "it", false: "them"}[v.Recheck == 1])
	case !v.Marked() && v.Unmarked > 0:
		v.Note = fmt.Sprintf("no verification markers - all %d line%s read as unverified; end a line with [verified YYYY-MM-DD by <command or doc>] or [assumed]",
			v.Unmarked, plural(v.Unmarked))
	}
	return v
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
