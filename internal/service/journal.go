package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// The journal's service surface. The write side matters more than the read
// one: the agent that just burned twenty minutes on a flaky subsystem is the
// only party who still has the symptom, the wrong conclusion and the real
// cause in hand, and a journal nobody writes to at the moment of the loss is
// a journal that stays empty.

// JournalAddInput is the argument set of pm_journal_add.
type JournalAddInput struct {
	Project         string   `json:"project" jsonschema:"Project slug or prefix"`
	Name            string   `json:"name" jsonschema:"Journal name, as declared in the project's project.yaml journals list (see pm_journal_list)"`
	Symptom         string   `json:"symptom" jsonschema:"What it looked like BEFORE the cause was known - what a future reader will recognise the situation by. Required."`
	FalseConclusion string   `json:"false_conclusion,omitempty" jsonschema:"The wrong belief the symptom produced (e.g. 'the rig is dead', 'the feature never mounts'). This is what makes an incident expensive - record it whenever there was one."`
	Cause           string   `json:"cause,omitempty" jsonschema:"What was actually going on, once established. Only what was verified - a guess recorded here reads as fact to the next session."`
	CostMin         int      `json:"cost_min,omitempty" jsonschema:"Minutes lost. Rough is fine; it is what turns 'this keeps happening' into something reviewable."`
	Fix             string   `json:"fix,omitempty" jsonschema:"The flow/tool change this argues for, and where it landed. LEAVE EMPTY if the fix has not been made - an entry with no fix is OPEN and forms the backlog. Never write 'n/a' or restate the cause here."`
	Tags            []string `json:"tags,omitempty" jsonschema:"Tags grouping incidents that share a cause. Reuse the tags already in the journal (pm_journal_list returns them) - a cluster is what argues for a tool fix, and a synonym splits one."`
	Session         string   `json:"session,omitempty" jsonschema:"Claude session id (pm session-id), so the entry can be traced back to a transcript"`
	Date            string   `json:"date,omitempty" jsonschema:"Back-date the entry as YYYY-MM-DD when seeding history. Omit for something that just happened."`
	Resolves        []string `json:"resolves,omitempty" jsonschema:"IDs of EARLIER entries this one closes (ids come from pm_journal_list). The journal is append-only, so an open entry can never be edited to carry its fix - the fix arrives as a new entry pointing back. This is how a review lands: one entry recording what was fixed AND closing what it fixed. An id that matches no entry is rejected."`
}

// JournalListInput is the argument set of pm_journal_list.
type JournalListInput struct {
	Project string `json:"project" jsonschema:"Project slug or prefix"`
	Name    string `json:"name,omitempty" jsonschema:"Journal name. Omit to list the journals declared for the project with their entry counts instead of reading one."`
	Open    bool   `json:"open,omitempty" jsonschema:"Only entries with no fix recorded (the backlog)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Max entries to return, newest first (default 20; 0 = default; hard cap 200). The result reports total vs shown."`
}

// DefaultJournalLimit is the entries-per-read default.
const DefaultJournalLimit = 20

// MaxJournalLimit mirrors MaxListLimit's rationale: an unbounded limit param
// turns the "raise limit" hint on a truncation note into a way to defeat the
// budget the default exists to enforce.
const MaxJournalLimit = 200

// JournalEntriesOutput is the read shape. Stats travel WITH the entries
// rather than in a separate call: the reason to read a journal at all is to
// see whether something recurs, and a caller who has to make a second call
// for the counts will skip it.
type JournalEntriesOutput struct {
	Project     string                 `json:"project"`
	Name        string                 `json:"name"`
	Subject     string                 `json:"subject,omitempty"`
	Total       int                    `json:"total"`
	Shown       int                    `json:"shown"`
	Open        int                    `json:"open"`
	ClosedLater int                    `json:"closed_later,omitempty"`
	CostMin     int                    `json:"cost_min,omitempty"`
	Tags        []storage.TagCount     `json:"tags,omitempty"`
	Months      []storage.MonthCount   `json:"months,omitempty"`
	Entries     []storage.Incident     `json:"entries"`
	Note        string                 `json:"note,omitempty"`
	Declared    []storage.JournalCount `json:"declared,omitempty"`
}

// JournalAddResult reports the entry just written and the recurrence it
// produced. Field order = the alphabetical key order of the map it
// replaced.
type JournalAddResult struct {
	Entry   storage.Incident `json:"entry"`
	Journal string           `json:"journal"`
	Note    string           `json:"note"`
	Open    int              `json:"open"`
	Project string           `json:"project"`
	Total   int              `json:"total"`
}

// JournalAdd appends one incident to a declared journal and reads the
// counts back so the caller sees the recurrence its own entry just
// produced. An undeclared name, a missing name or a malformed date is a
// *ValidationError.
func JournalAdd(store storage.TaskStore, in JournalAddInput) (*JournalAddResult, error) {
	slug, proj, err := resolveJournalProject(store, in.Project)
	if err != nil {
		return nil, err
	}
	subject, err := resolveJournalSubject(proj, in.Name)
	if err != nil {
		return nil, err
	}
	incident := storage.Incident{
		Symptom:         in.Symptom,
		FalseConclusion: in.FalseConclusion,
		Cause:           in.Cause,
		CostMin:         in.CostMin,
		Fix:             in.Fix,
		Tags:            in.Tags,
		Session:         in.Session,
		Resolves:        in.Resolves,
	}
	if in.Date != "" {
		ts, derr := parseJournalDate(in.Date)
		if derr != nil {
			return nil, derr
		}
		incident.TS = ts
	}
	if err := storage.AppendIncident(store.ProjectDir(slug), subject.Name, &incident); err != nil {
		return nil, err
	}

	incidents, _ := storage.ReadIncidents(store.ProjectDir(slug), subject.Name)
	st := storage.AggregateIncidents(incidents)
	return &JournalAddResult{
		Project: slug,
		Journal: subject.Name,
		Entry:   incident,
		Total:   st.Total,
		Open:    st.Open,
		Note:    journalAddNote(incident, st),
	}, nil
}

// JournalList reads a journal newest first under the entries budget, stats
// attached; without a name it lists the project's declared journals with
// their counts.
func JournalList(store storage.TaskStore, in JournalListInput) (*JournalEntriesOutput, error) {
	slug, proj, err := resolveJournalProject(store, in.Project)
	if err != nil {
		return nil, err
	}
	counts, err := storage.JournalCounts(store.ProjectDir(slug), proj)
	if err != nil {
		return nil, err
	}

	if in.Name == "" {
		out := &JournalEntriesOutput{Project: slug, Declared: counts, Entries: []storage.Incident{}}
		if len(counts) == 0 {
			out.Note = "no journals declared for this project - add a `journals:` list (name + subject) to project.yaml to start tracking a subsystem"
		}
		return out, nil
	}

	subject, err := resolveJournalSubject(proj, in.Name)
	if err != nil {
		return nil, err
	}
	incidents, err := storage.ReadIncidents(store.ProjectDir(slug), subject.Name)
	if err != nil {
		return nil, err
	}
	st := storage.AggregateIncidents(incidents)

	shown := newestFirst(incidents, in.Open)
	limit := in.Limit
	capped := false
	if limit <= 0 {
		limit = DefaultJournalLimit
	} else if limit > MaxJournalLimit {
		limit = MaxJournalLimit
		capped = true
	}
	total := len(shown)
	entryWord := "entries"
	if in.Open {
		entryWord = "open entries"
	}
	note := ""
	switch {
	case capped && total > limit:
		shown = shown[:limit]
		note = fmt.Sprintf("%d of %d %s shown (newest first) - limit capped at %d; use `pm journal show %s --limit 0 --project %s` for the full history", len(shown), total, entryWord, MaxJournalLimit, subject.Name, slug)
	case capped:
		note = fmt.Sprintf("all %d %s shown; limit capped at %d", total, entryWord, MaxJournalLimit)
	case total > limit:
		shown = shown[:limit]
		// Silent truncation would read as "that is the whole history",
		// which is exactly the wrong conclusion for a recurrence record.
		note = fmt.Sprintf("%d of %d entries shown (newest first) - raise limit for the rest", len(shown), total)
	}

	return &JournalEntriesOutput{
		Project:     slug,
		Name:        subject.Name,
		Subject:     subject.Subject,
		Total:       st.Total,
		Shown:       len(shown),
		Open:        st.Open,
		ClosedLater: st.ClosedLater,
		CostMin:     st.CostMin,
		Tags:        st.Tags,
		Months:      st.Months,
		Entries:     shown,
		Note:        note,
	}, nil
}

// journalAddNote tells the writer what their entry means in context. "5th
// entry, 3 open, 2 share this tag" is the sentence that turns a log into an
// argument for fixing the tool - and it costs one line at the moment someone
// is already paying attention.
func journalAddNote(in storage.Incident, st storage.JournalStats) string {
	parts := []string{fmt.Sprintf("entry %d in this journal", st.Total)}
	if st.Open > 0 {
		parts = append(parts, fmt.Sprintf("%d open (no fix recorded)", st.Open))
	}
	for _, tag := range in.Tags {
		for _, tc := range st.Tags {
			if tc.Tag == tag && tc.Count > 1 {
				parts = append(parts, fmt.Sprintf("%d entries now share tag %q", tc.Count, tag))
			}
		}
	}
	if len(in.Resolves) > 0 {
		parts = append(parts, fmt.Sprintf("closed %d earlier entr%s", len(in.Resolves),
			map[bool]string{true: "y", false: "ies"}[len(in.Resolves) == 1]))
	}
	if in.Open() {
		parts = append(parts, "this one is OPEN - close it later with a new entry naming it in resolves")
	}
	return strings.Join(parts, "; ")
}

// newestFirst orders for reading (the file itself stays in append order),
// putting undated entries last for the same reason the CLI does.
func newestFirst(incidents []storage.Incident, openOnly bool) []storage.Incident {
	resolved := storage.ResolvedIDs(incidents)
	out := make([]storage.Incident, 0, len(incidents))
	for _, in := range incidents {
		if openOnly && !storage.StillOpen(in, resolved) {
			continue
		}
		out = append(out, in)
	}
	sort.SliceStable(out, func(i, j int) bool { return journalLess(out[i], out[j]) })
	return out
}

// parseJournalDate accepts the day only. Seeding history is a first-class use -
// a journal starts life holding the incidents that argued for its existence -
// and nobody remembers the hour of an incident from three weeks ago.
func parseJournalDate(s string) (string, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", validation(fmt.Errorf("date %q: want YYYY-MM-DD", s))
	}
	return t.Format(time.RFC3339), nil
}

func journalLess(a, b storage.Incident) bool {
	at, aOK := a.When()
	bt, bOK := b.When()
	if aOK != bOK {
		return aOK
	}
	if !aOK {
		return false
	}
	return at.After(bt)
}

func resolveJournalProject(store storage.TaskStore, project string) (string, *storage.Project, error) {
	slug, err := store.ResolveProject(project)
	if err != nil {
		return "", nil, err
	}
	proj, err := store.GetProject(slug)
	if err != nil {
		return "", nil, err
	}
	return slug, proj, nil
}

// resolveJournalSubject mirrors the CLI: an undeclared name is an error, never
// an implicit create. An agent inventing a plausible-looking journal name would
// start a second history nobody ever reads.
func resolveJournalSubject(proj *storage.Project, name string) (*storage.JournalSubject, error) {
	if name == "" {
		return nil, validation(fmt.Errorf("name is required"))
	}
	if s := proj.Journal(name); s != nil {
		return s, nil
	}
	declared := proj.JournalNames()
	if len(declared) == 0 {
		return nil, validation(fmt.Errorf("no journals declared for this project - add a `journals:` list (name + subject) to project.yaml first"))
	}
	return nil, validation(fmt.Errorf("no journal %q in this project (declared: %s)", name, strings.Join(declared, ", ")))
}
