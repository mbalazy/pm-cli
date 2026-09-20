package storage

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A project timeline is the append-only record of what happened TO a project -
// a direction change, a client decision, a milestone, a document arriving -
// plus, every so often, a dated `state` entry saying where the project stands.
//
// It exists because that history had nowhere to live: it landed in task Logs
// (a journal per task, so "what happened this week" meant opening four tasks),
// in project.yaml notes ("stan 27.08" in the field meant for current truth,
// where overwriting it loses the history), and in documents rewritten in place.
//
// The boundary that keeps it from becoming one more pile: a task Log records
// what happened IN a task; the timeline records what happened TO the project.
// The change feed (machine events from git, GitHub, pm) is never imported.
//
// A state is a snapshot entry, never an overwritten field: a new state is a new
// entry and older states stay, so "where did we stand on 27.08" is answerable.
// The default read is the latest state PLUS every entry after it - a state lies
// from the first entry written after it - and the stale signal (too many
// entries, or too many days, since that state) is what asks for a new one. That
// signal is the growth control; nothing is ever summarised or deleted.
//
// Storage: JSONL, one file per month, <projectDir>/.timeline/<YYYY-MM>.jsonl,
// the month taken from the entry's ts. There is exactly one timeline per
// project, so unlike subsystem journals nothing is declared in project.yaml.

// Timeline entry kinds - a closed set.
const (
	TimelineEvent    = "event"
	TimelineDecision = "decision"
	TimelineState    = "state"
)

// TimelineKinds lists the legal kinds in the order they are documented.
var TimelineKinds = []string{TimelineEvent, TimelineDecision, TimelineState}

// Default-read thresholds. The accepted design says "about 10 entries or 7
// days"; they live here so every surface (CLI, MCP, cockpit) agrees.
const (
	// TimelineStaleEntries entries after the latest state make it stale.
	TimelineStaleEntries = 10
	// TimelineStaleDays whole days since the latest state make it stale.
	TimelineStaleDays = 7
	// TimelineNoStateLimit is how many of the newest entries the default read
	// shows when the project has no state at all.
	TimelineNoStateLimit = 10
)

// TimelineEntry is one line of a project timeline: an EVENT, never edited.
type TimelineEntry struct {
	// ID lets a later entry (or a task, or a doc) point at this one. Derived
	// from ts+kind+text rather than minted from a counter, so a line written by
	// hand without an id gets the same one on read that a write would stamp.
	ID string `json:"id"`
	// TS is RFC3339, stamped by AppendTimelineEntry when empty. Its month picks
	// the file.
	TS string `json:"ts"`
	// Kind is one of TimelineKinds.
	Kind string `json:"kind"`
	// Text is the entry itself. One to three sentences for an event or a
	// decision; for a state, a snapshot that fits on a screen (10-20 lines:
	// where we stand, what blocks, what is next, open questions, links). Long
	// content stays in the task or the document; the entry points at it.
	Text string `json:"text"`
	// Refs are free references - task ids, paths, URLs. Never validated.
	Refs    []string `json:"refs,omitempty"`
	Session string   `json:"session,omitempty"` // Claude session id, if written from one
}

// When parses TS. False for a missing or unparseable timestamp, which a
// hand-edited line can carry - every ordering decision says where those go.
func (e TimelineEntry) When() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, e.TS)
	return t, err == nil
}

// timelineEntryID derives a short, stable handle: the entry's own calendar day
// plus a hash of the fields that make the entry what it is (the incidentID
// shape). The day is taken in the ts's OWN offset, not UTC: a back-dated
// entry for the 1st written at +02:00 would otherwise carry the 31st in its
// id while every listing prints the 1st, and in a timeline the date is the
// point.
func timelineEntryID(ts, kind, text string) string {
	day := "undated"
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		day = t.Format("20060102")
	}
	sum := sha256.Sum256([]byte(ts + "\x00" + kind + "\x00" + text))
	return fmt.Sprintf("%s-%x", day, sum[:2])
}

// ValidateTimelineKind rejects anything outside TimelineKinds.
func ValidateTimelineKind(kind string) error {
	for _, k := range TimelineKinds {
		if kind == k {
			return nil
		}
	}
	return fmt.Errorf("invalid timeline kind %q (want one of: %s)", kind, strings.Join(TimelineKinds, ", "))
}

// TimelineBackdate turns a `--date YYYY-MM-DD` value into the ts a back-dated
// entry carries: midnight of that day in the local zone. The one exception is
// TODAY (in now's zone), which answers now itself. An entry dated today is not
// history - it is being written now - and midnight would sort it BEFORE every
// entry written earlier the same day without the flag: as a state it would
// hide behind an older state in the default read (observed 2026-09-16: a state
// added with `--date <today>` at 10:00 read as older than the bootstrap's
// state of 09:02). A malformed date is an error naming the wanted layout.
func TimelineBackdate(date string, now time.Time) (string, error) {
	t, err := time.ParseInLocation(stampDateLayout, date, now.Location())
	if err != nil {
		return "", fmt.Errorf("--date %q: want YYYY-MM-DD", date)
	}
	if t.Format(stampDateLayout) == now.Format(stampDateLayout) {
		return now.Format(time.RFC3339), nil
	}
	return t.Format(time.RFC3339), nil
}

// TimelineDir is where a project's timeline files live.
func TimelineDir(projectDir string) string {
	return filepath.Join(projectDir, ".timeline")
}

// TimelineFilePath is the JSONL file for one month (YYYY-MM).
func TimelineFilePath(projectDir, month string) string {
	return filepath.Join(TimelineDir(projectDir), month+".jsonl")
}

// AppendTimelineEntry validates the entry, stamps TS (now, local zone) and ID
// when unset, and appends it as one line to its month's file. Append-only: no
// code path rewrites or truncates a timeline file. Everything is checked BEFORE
// the directory or the file is touched, so a rejected entry leaves no trace.
func AppendTimelineEntry(projectDir string, e *TimelineEntry) error {
	e.Kind = strings.TrimSpace(e.Kind)
	if err := ValidateTimelineKind(e.Kind); err != nil {
		return err
	}
	e.Text = strings.TrimSpace(e.Text)
	if e.Text == "" {
		return fmt.Errorf("a timeline entry needs text")
	}
	if e.TS == "" {
		e.TS = time.Now().Format(time.RFC3339)
	}
	when, ok := e.When()
	if !ok {
		return fmt.Errorf("timeline entry ts %q is not RFC3339", e.TS)
	}
	// An entry records something that already happened. A future date - a
	// typo'd back-date, typically - would sit after every real entry: as a
	// state it would empty the default read and mute the stale signal until
	// that day, and an append-only file offers no way to correct it.
	now := time.Now()
	if !when.Before(time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())) {
		return fmt.Errorf("timeline entry dated %s is in the future - an entry records something that already happened", when.Format("2006-01-02"))
	}
	if err := ValidateStateMarkers(e.Kind, e.Text, now); err != nil {
		return err
	}
	e.Refs = cleanRefs(e.Refs)
	if e.ID == "" {
		e.ID = timelineEntryID(e.TS, e.Kind, e.Text)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(TimelineDir(projectDir), 0755); err != nil {
		return err
	}
	// The month is the one the writer's own offset puts the entry in, so an
	// entry written at 00:30 on the 1st lands in the month its author saw.
	f, err := os.OpenFile(TimelineFilePath(projectDir, when.Format("2006-01")), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func cleanRefs(refs []string) []string {
	var out []string
	for _, r := range refs {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// ReadTimeline returns every entry of a project's timeline in chronological
// order (oldest first), across all month files; entries with an unparseable ts
// sort LAST, in file order. A project with no timeline is empty, not an error,
// and reading never creates the directory. Unparseable lines, lines with no
// text and lines of an unknown kind are skipped so one bad hand edit never
// hides the rest of the history.
func ReadTimeline(projectDir string) ([]TimelineEntry, error) {
	files, err := os.ReadDir(TimelineDir(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".jsonl") {
			names = append(names, f.Name())
		}
	}
	sort.Strings(names)

	var out []TimelineEntry
	for _, name := range names {
		entries, err := readTimelineFile(filepath.Join(TimelineDir(projectDir), name))
		if err != nil {
			return nil, err
		}
		out = append(out, entries...)
	}
	sortTimeline(out, false)
	return out, nil
}

func readTimelineFile(path string) ([]TimelineEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []TimelineEntry
	// A reader, not a Scanner: the write side accepts any text length, so a
	// line-length cap here would turn one long state (JSON escaping alone
	// grows pasted HTML sixfold) into a timeline nobody can read again.
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var e TimelineEntry
		if len(line) > 0 && json.Unmarshal(line, &e) == nil && strings.TrimSpace(e.Text) != "" && ValidateTimelineKind(e.Kind) == nil {
			if e.ID == "" {
				e.ID = timelineEntryID(e.TS, e.Kind, e.Text)
			}
			out = append(out, e)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// sortTimeline orders by the PARSED instant (RFC3339 permits offsets, so the
// raw strings do not compare), undated entries last in either direction, ties
// in their existing order.
func sortTimeline(entries []TimelineEntry, newestFirst bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, aOK := entries[i].When()
		b, bOK := entries[j].When()
		if aOK != bOK {
			return aOK
		}
		if !aOK {
			return false
		}
		if newestFirst {
			return a.After(b)
		}
		return a.Before(b)
	})
}

// TimelineRead is the default read of a project timeline: the latest state in
// full plus every entry after it, never the state alone.
type TimelineRead struct {
	// State is the latest state entry, nil when the project has none.
	State *TimelineEntry `json:"state"`
	// Since holds the entries after State, oldest first. With no state it
	// holds the newest TimelineNoStateLimit entries, still oldest first.
	Since []TimelineEntry `json:"since"`
	// Stale is set when EntriesSince or DaysSince reached its threshold - the
	// signal to write a new state. Never set without a state.
	Stale bool `json:"stale"`
	// EntriesSince counts the entries after the state (all of Since).
	EntriesSince int `json:"entries_since"`
	// DaysSince is whole days from the state's ts to now; 0 without a state.
	DaysSince int `json:"days_since"`
	// Total is every entry in the timeline, states included.
	Total int `json:"total"`
	// Verification is the state's provenance picture (verified / due for a
	// re-check / assumed / unmarked lines, the re-check lines listed); nil
	// without a state.
	Verification *TimelineVerification `json:"verification,omitempty"`
	// Note says what the read is when it is not the plain case (no state yet,
	// empty timeline, stale).
	Note string `json:"note,omitempty"`
}

// TimelineDefaultRead computes the default read from chronologically ordered
// entries (ReadTimeline's order) at the given instant. Pure, so the thresholds
// are testable with a fixed clock.
func TimelineDefaultRead(entries []TimelineEntry, now time.Time) TimelineRead {
	r := TimelineRead{Since: []TimelineEntry{}, Total: len(entries)}
	if len(entries) == 0 {
		r.Note = "no timeline entries yet"
		return r
	}

	// The latest DATED state wins; an undated state (a hand-edited line) only
	// counts when no dated one exists, so one bad line cannot hide the real
	// latest state behind it.
	idx := -1
	for i, e := range entries {
		if e.Kind != TimelineState {
			continue
		}
		if _, ok := e.When(); ok {
			idx = i
		}
	}
	if idx < 0 {
		for i, e := range entries {
			if e.Kind == TimelineState {
				idx = i
			}
		}
	}

	if idx < 0 {
		start := max(len(entries)-TimelineNoStateLimit, 0)
		r.Since = append(r.Since, entries[start:]...)
		r.Note = fmt.Sprintf("no state yet - the newest %d of %d entries; write a state entry saying where the project stands", len(r.Since), len(entries))
		return r
	}

	state := entries[idx]
	r.State = &state
	v := VerifyState(state.Text, now)
	r.Verification = &v
	r.Since = append(r.Since, entries[idx+1:]...)
	r.EntriesSince = len(r.Since)
	if when, ok := state.When(); ok && now.After(when) {
		r.DaysSince = int(now.Sub(when).Hours() / 24)
	}
	if r.EntriesSince >= TimelineStaleEntries || r.DaysSince >= TimelineStaleDays {
		r.Stale = true
		r.Note = fmt.Sprintf("stale: %d entr%s and %d day%s since the state - time to write a new state",
			r.EntriesSince, map[bool]string{true: "y", false: "ies"}[r.EntriesSince == 1],
			r.DaysSince, map[bool]string{true: "", false: "s"}[r.DaysSince == 1])
	}
	return r
}

// ReadTimelineDefault reads a project's timeline and computes its default read.
func ReadTimelineDefault(projectDir string, now time.Time) (TimelineRead, error) {
	entries, err := ReadTimeline(projectDir)
	if err != nil {
		return TimelineRead{}, err
	}
	return TimelineDefaultRead(entries, now), nil
}

// FilterTimeline returns the entries at or after since (zero = no bound; an
// undated entry never matches a bound) and of the given kind ("" = any),
// newest first with undated entries last.
func FilterTimeline(entries []TimelineEntry, since time.Time, kind string) []TimelineEntry {
	out := []TimelineEntry{}
	for _, e := range entries {
		if kind != "" && e.Kind != kind {
			continue
		}
		if !since.IsZero() {
			when, ok := e.When()
			if !ok || when.Before(since) {
				continue
			}
		}
		out = append(out, e)
	}
	sortTimeline(out, true)
	return out
}
