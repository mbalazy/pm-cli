package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// The project timeline's service surface (see internal/storage/timeline.go):
// what happened TO a project, and dated state snapshots of where it stands.
// The parameter descriptions are part of the agent contract - they carry the
// kinds and the "a state fits on a screen" rule, because the description is
// what an agent reads at the moment it decides what to write.

// TimelineAddInput is the argument set of pm_timeline_add.
type TimelineAddInput struct {
	Project string   `json:"project" jsonschema:"Project slug or prefix"`
	Kind    string   `json:"kind" jsonschema:"event (something happened to the project - one to three sentences), decision (one sentence plus refs to the full record in a task or doc), or state (a dated snapshot of where the project stands that fits on a screen: 10-20 lines - where we stand, what blocks, what is next, open questions, links)"`
	Text    string   `json:"text" jsonschema:"The entry. Long material stays in the task or document; the entry points at it through refs. In a state, end every line with its provenance: [verified YYYY-MM-DD by <command or doc>] for a fact checked in this session, [assumed] for one that was not - a line with no marker is read as unverified, a [verified] marker without a day (or with a future day) is refused."`
	Refs    []string `json:"refs,omitempty" jsonschema:"References - task ids, doc paths, URLs"`
	Session string   `json:"session,omitempty" jsonschema:"Claude session id (pm session-id), so the entry can be traced back to a transcript"`
}

// TimelineListInput is the argument set of pm_timeline_list.
type TimelineListInput struct {
	Project string `json:"project" jsonschema:"Project slug or prefix"`
	Since   string `json:"since,omitempty" jsonschema:"Only entries on or after this day (YYYY-MM-DD). Any of since/kind/limit switches the result from the default read to a list."`
	Kind    string `json:"kind,omitempty" jsonschema:"Only entries of this kind: event, decision or state"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Max entries to return, newest first (default 20; hard cap 200). The result reports total vs shown."`
}

const (
	// DefaultTimelineLimit is the entries-per-list default.
	DefaultTimelineLimit = 20
	// MaxTimelineLimit caps limit, for the MaxListLimit reason.
	MaxTimelineLimit = 200
	// ContextTimelineLimit caps the entries after the state that pm_context
	// carries. The state itself is never cut.
	ContextTimelineLimit = 20
)

// TimelineAddResult reports the written entry and where the timeline stands
// right after it, so the writer sees the stale signal at the moment it writes.
type TimelineAddResult struct {
	Project      string                `json:"project"`
	Entry        storage.TimelineEntry `json:"entry"`
	Stale        bool                  `json:"stale"`
	EntriesSince int                   `json:"entries_since"`
	DaysSince    int                   `json:"days_since"`
	Note         string                `json:"note"`
}

// TimelineReadResult is the default read of a project timeline - the latest
// state plus every entry after it - as pm_timeline_list (no filters) and the
// cockpit's timeline endpoint return it.
type TimelineReadResult struct {
	Project string `json:"project"`
	storage.TimelineRead
}

// TimelineEntriesResult is pm_timeline_list's filtered shape. Total counts the
// entries matching since/kind - the set limit cuts.
type TimelineEntriesResult struct {
	Project string                  `json:"project"`
	Entries []storage.TimelineEntry `json:"entries"`
	Total   int                     `json:"total"`
	Shown   int                     `json:"shown"`
	Note    string                  `json:"note,omitempty"`
}

// TimelineAdd appends one entry to a project's timeline. An unknown kind or
// an empty text is a *ValidationError and writes nothing.
func TimelineAdd(store storage.TaskStore, in TimelineAddInput) (*TimelineAddResult, error) {
	slug, err := store.ResolveProject(in.Project)
	if err != nil {
		return nil, err
	}
	kind := strings.TrimSpace(in.Kind)
	if err := storage.ValidateTimelineKind(kind); err != nil {
		return nil, validation(err)
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, validation(fmt.Errorf("text is required - what happened to the project, or where it stands"))
	}
	// A malformed [verified] marker is the caller's mistake (a 400 on the
	// web, not a 500); the store checks it again before writing.
	if err := storage.ValidateStateMarkers(kind, in.Text, time.Now()); err != nil {
		return nil, validation(err)
	}
	entry := storage.TimelineEntry{Kind: kind, Text: in.Text, Refs: in.Refs, Session: in.Session}
	dir := store.ProjectDir(slug)
	if err := storage.AppendTimelineEntry(dir, &entry); err != nil {
		return nil, err
	}
	res := &TimelineAddResult{Project: slug, Entry: entry}
	if r, err := storage.ReadTimelineDefault(dir, time.Now()); err == nil {
		res.Stale, res.EntriesSince, res.DaysSince = r.Stale, r.EntriesSince, r.DaysSince
		res.Note = timelineAddNote(entry, r)
	}
	return res, nil
}

func timelineAddNote(e storage.TimelineEntry, r storage.TimelineRead) string {
	switch {
	case e.Kind == storage.TimelineState:
		note := "state recorded - the default read starts from it now"
		if r.Verification != nil && r.Verification.Note != "" {
			note += "; " + r.Verification.Note
		}
		return note
	case r.State == nil:
		return "no state yet - write a state entry saying where the project stands"
	case r.Stale:
		return r.Note
	default:
		return fmt.Sprintf("%d entr%s since the state of %s", r.EntriesSince,
			map[bool]string{true: "y", false: "ies"}[r.EntriesSince == 1], stateDate(*r.State))
	}
}

// TimelineDefaultRead returns a project's default read. An unknown project is
// storage.ErrProjectNotFound; a project with no timeline is an empty read.
func TimelineDefaultRead(store storage.TaskStore, project string) (*TimelineReadResult, error) {
	slug, err := store.ResolveProject(project)
	if err != nil {
		return nil, err
	}
	r, err := storage.ReadTimelineDefault(store.ProjectDir(slug), time.Now())
	if err != nil {
		return nil, err
	}
	return &TimelineReadResult{Project: slug, TimelineRead: r}, nil
}

// TimelineList returns the default read (*TimelineReadResult) when no since,
// kind or limit is given, else the matching entries newest first
// (*TimelineEntriesResult) under the limit budget.
func TimelineList(store storage.TaskStore, in TimelineListInput) (any, error) {
	// Trimmed like TimelineAdd trims it, so a kind accepted on write is
	// never refused on read.
	in.Kind = strings.TrimSpace(in.Kind)
	if in.Since == "" && in.Kind == "" && in.Limit == 0 {
		return TimelineDefaultRead(store, in.Project)
	}
	slug, err := store.ResolveProject(in.Project)
	if err != nil {
		return nil, err
	}
	var since time.Time
	if in.Since != "" {
		if since, err = time.ParseInLocation("2006-01-02", in.Since, time.Local); err != nil {
			return nil, validation(fmt.Errorf("since %q: want YYYY-MM-DD", in.Since))
		}
	}
	if in.Kind != "" {
		if err := storage.ValidateTimelineKind(in.Kind); err != nil {
			return nil, validation(err)
		}
	}
	entries, err := storage.ReadTimeline(store.ProjectDir(slug))
	if err != nil {
		return nil, err
	}
	matched := storage.FilterTimeline(entries, since, in.Kind)

	limit := in.Limit
	capped := false
	if limit <= 0 {
		limit = DefaultTimelineLimit
	} else if limit > MaxTimelineLimit {
		limit, capped = MaxTimelineLimit, true
	}
	out := &TimelineEntriesResult{Project: slug, Entries: matched, Total: len(matched)}
	if len(matched) > limit {
		out.Entries = matched[:limit]
		out.Note = fmt.Sprintf("%d of %d matching entries shown (newest first) - raise limit or narrow since for the rest", limit, len(matched))
		if capped {
			out.Note += fmt.Sprintf("; limit capped at %d", MaxTimelineLimit)
		}
	} else if capped {
		out.Note = fmt.Sprintf("all %d matching entries shown; limit capped at %d", len(matched), MaxTimelineLimit)
	}
	out.Shown = len(out.Entries)
	return out, nil
}

// TimelineContext is the timeline block of a project-scoped pm_context: the
// default read under a budget - the state whole, the newest entries after it.
type TimelineContext struct {
	State *storage.TimelineEntry  `json:"state"`
	Since []storage.TimelineEntry `json:"since"`
	// SinceOmitted counts the oldest entries after the state left out by
	// ContextTimelineLimit.
	SinceOmitted int `json:"since_omitted,omitempty"`
	// Verification is the state's provenance picture: counts of verified /
	// to-recheck / assumed / unmarked lines and the lines due for a re-check.
	Verification *storage.TimelineVerification `json:"verification,omitempty"`
	Stale        bool                          `json:"stale"`
	EntriesSince int                           `json:"entries_since"`
	DaysSince    int                           `json:"days_since"`
	Total        int                           `json:"total"`
	Note         string                        `json:"note"`
}

// contextTimeline builds the pm_context block; nil when the project has no
// timeline entries (no data = no key, the journals rule) or it cannot be read.
func contextTimeline(dir string, now time.Time) *TimelineContext {
	r, err := storage.ReadTimelineDefault(dir, now)
	if err != nil || r.Total == 0 {
		return nil
	}
	c := &TimelineContext{
		State:        r.State,
		Since:        r.Since,
		Verification: r.Verification,
		Stale:        r.Stale,
		EntriesSince: r.EntriesSince,
		DaysSince:    r.DaysSince,
		Total:        r.Total,
	}
	if r.State != nil && len(c.Since) > ContextTimelineLimit {
		c.SinceOmitted = len(c.Since) - ContextTimelineLimit
		c.Since = c.Since[c.SinceOmitted:]
	}
	notes := []string{"what happened TO this project: the latest state in full plus the entries after it, oldest first - read them together, a state alone goes out of date with the first entry after it. A state line ending in [verified <day> by <source>] was checked on that day; [assumed] was not; a line with neither is unverified - `verification` counts them and lists the verified lines due for a re-check. pm_timeline_list with since/kind reaches older entries; record a project-level event, decision or new state with pm_timeline_add"}
	if c.SinceOmitted > 0 {
		notes = append(notes, fmt.Sprintf("the %d oldest entries after the state are omitted here", c.SinceOmitted))
	}
	if r.Verification != nil && r.Verification.Note != "" {
		notes = append(notes, r.Verification.Note)
	}
	if r.Note != "" {
		notes = append(notes, r.Note)
	}
	c.Note = strings.Join(notes, "; ")
	return c
}

// timelineStatePointer is the cross-project rollup's one line per project:
// "<date>: <first line of the latest state>", "(stale)" appended when due.
// Empty when the project has no state.
func timelineStatePointer(dir string, now time.Time) string {
	r, err := storage.ReadTimelineDefault(dir, now)
	if err != nil || r.State == nil {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(r.State.Text), "\n")
	if runes := []rune(first); len(runes) > 160 {
		first = string(runes[:160]) + "..."
	}
	s := stateDate(*r.State) + ": " + first
	if r.Stale {
		s += " (stale)"
	}
	if r.Verification != nil && r.Verification.Recheck > 0 {
		s += fmt.Sprintf(" (%d to recheck)", r.Verification.Recheck)
	}
	return s
}

func stateDate(e storage.TimelineEntry) string {
	if t, ok := e.When(); ok {
		return t.Local().Format("2006-01-02")
	}
	return "undated"
}
