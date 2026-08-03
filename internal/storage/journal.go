package storage

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A subsystem journal is the append-only running record of how ONE chosen,
// repeatedly-troublesome subsystem actually behaves in a project: what bit us,
// when, what it looked like, what we wrongly concluded, what the real cause
// was, and which flow/tool change it argues for.
//
// It exists because the same problems recur and are paid for again every time
// (the first instance is an iOS simulator rig whose incidents had accumulated
// as 15 separate Claude memory files plus a hand-grown section in a repo doc -
// none of them dated, counted, or connected to a fix).
//
// The line it must not blur: a memory file or a doc holds a RULE - current
// truth, rewritten when it turns out wrong. A journal entry holds an EVENT -
// dated, append-only, never rewritten. A rule can never answer "fifth time this
// month", and a journal never replaces the rule. An entry POINTS at the fix it
// produced, and an entry with no Fix recorded IS the backlog.
//
// The mechanism is deliberately agnostic: WHICH subsystem a project journals is
// project.yaml configuration (see Project.Journals), and nothing here knows
// what a simulator, a flaky test suite or a payment sandbox is.
//
// Storage mirrors the executor journal one directory over: append-only JSONL,
// one file per subject, under <projectDir>/.journal/<name>.jsonl.

// JournalSubject declares one journaled subsystem in project.yaml.
type JournalSubject struct {
	// Name is the journal's identifier AND its file name, so it is validated
	// as a path component (ValidateJournalName) wherever it is written.
	Name string `yaml:"name"`
	// Subject is the one-line answer to "what is being tracked here" - shown
	// by `pm journal list` so a journal nobody has touched in months still
	// says what it is for.
	Subject string `yaml:"subject,omitempty"`
}

// Incident is one entry: an EVENT that happened, never edited afterwards.
type Incident struct {
	// ID identifies this entry so a LATER entry can close it (see Resolves).
	// Derived from ts+symptom rather than minted from a counter, which means
	// ReadIncidents can compute it for lines written before the field existed
	// - the whole history stays referenceable without rewriting one byte of an
	// append-only file. Stamped on write too, so the file is self-describing.
	ID string `json:"id,omitempty"`
	TS string `json:"ts"` // RFC3339, stamped by AppendIncident if empty
	// Symptom is what it looked like from the outside, before the cause was
	// known - the thing a future reader will recognise the situation by.
	Symptom string `json:"symptom"`
	// FalseConclusion is the wrong belief the symptom produced ("the rig is
	// dead", "the feature never mounts"). This is the field that carries the
	// actual cost: a subsystem that fails LOUDLY is cheap, one that produces a
	// confident wrong answer is what burns afternoons.
	FalseConclusion string `json:"false_conclusion,omitempty"`
	// Cause is what was actually going on, once established.
	Cause string `json:"cause,omitempty"`
	// CostMin is minutes lost, the unit that turns "I keep having problems
	// with it" into something reviewable.
	CostMin int `json:"cost_min,omitempty"`
	// Fix is the flow/tool change this incident argues for, and where it
	// landed. EMPTY MEANS OPEN - the open set is the derived backlog, which is
	// why this is never filled in with "n/a" or a restatement of the cause.
	Fix string `json:"fix,omitempty"`
	// Tags group incidents that share a cause; recurrence across a tag is the
	// signal that a tool fix is overdue.
	Tags    []string `json:"tags,omitempty"`
	Session string   `json:"session,omitempty"` // Claude session id, if written from one
	// Resolves lists the IDs of EARLIER entries this one closes. Append-only
	// storage means an open entry can never be edited to carry its fix, so the
	// fix arrives as a new event pointing back - which is also exactly what a
	// periodic review produces ("reviewed up to <date>, these are now fixed").
	// Without this the open count could only ever grow and the backlog would
	// rot, which is the failure the journal exists to prevent.
	Resolves []string `json:"resolves,omitempty"`
}

// SelfClosed reports whether the entry arrived with its own fix recorded. It is
// only half the answer - use OpenIncidents/StillOpen for the real one, which
// also accounts for a later entry having closed this one.
func (i Incident) SelfClosed() bool { return strings.TrimSpace(i.Fix) != "" }

// Open reports whether the entry, read ALONE, still argues for an unmade
// change. Callers holding the whole journal must use StillOpen instead.
func (i Incident) Open() bool { return !i.SelfClosed() }

// incidentID derives a short, stable handle: the UTC day (so it reads as
// roughly when, and sorts) plus a hash of the fields that make the entry what
// it is. Deterministic on purpose - the same entry gets the same id whether it
// was stamped at write time or computed while reading an older line.
func incidentID(ts, symptom string) string {
	day := "undated"
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		day = t.UTC().Format("20060102")
	}
	sum := sha256.Sum256([]byte(ts + "\x00" + symptom))
	return fmt.Sprintf("%s-%x", day, sum[:2])
}

// ResolvedIDs collects every entry id that some OTHER entry claims to close.
func ResolvedIDs(incidents []Incident) map[string]bool {
	out := map[string]bool{}
	for _, in := range incidents {
		for _, id := range in.Resolves {
			if id = strings.TrimSpace(id); id != "" {
				out[id] = true
			}
		}
	}
	return out
}

// StillOpen is the real open test: no fix of its own, and nobody has since
// closed it.
func StillOpen(in Incident, resolved map[string]bool) bool {
	return in.Open() && !resolved[in.ID]
}

// When parses TS. The bool is false for an entry whose timestamp is missing or
// unparseable - a real possibility, since the file is plain JSONL a human may
// have hand-seeded, and every ordering decision has to say where those go
// rather than let string comparison decide it (which sorted "not a timestamp"
// above every real date, straight to the top of the actionable list).
func (i Incident) When() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, i.TS)
	return t, err == nil
}

// Month returns the incident's YYYY-MM bucket, or "" if TS is unparseable.
func (i Incident) Month() string {
	t, ok := i.When()
	if !ok {
		return ""
	}
	return t.Format("2006-01")
}

// journalDir is where a project's subsystem journals live.
func journalDir(projectDir string) string {
	return filepath.Join(projectDir, ".journal")
}

// JournalFilePath is the JSONL path for one journaled subject.
func JournalFilePath(projectDir, name string) string {
	return filepath.Join(journalDir(projectDir), name+".jsonl")
}

// ValidateJournalName checks that a journal name is safe as a file name. The
// name comes from project.yaml, which is hand-edited, and it becomes a path
// component - the same reason ValidateSlug and ValidateTaskID exist. Reusing
// the slug rule keeps journal names in the same lowercase shape as project
// slugs rather than inventing a third alphabet.
func ValidateJournalName(name string) error {
	if err := ValidateSlug(name); err != nil {
		return fmt.Errorf("invalid journal name: %w", err)
	}
	return nil
}

// AppendIncident appends one incident to a subject's journal, stamping TS if
// unset. Append-only: the file is never rewritten or truncated.
//
// Unlike the executor journal - pure best-effort observability written from
// inside a run - this one is called BY a human or an agent that has just spent
// the time being recorded, so a failed write is returned rather than swallowed:
// silently losing the entry loses the only record that the cost was ever paid.
func AppendIncident(projectDir, name string, in *Incident) error {
	if err := ValidateJournalName(name); err != nil {
		return err
	}
	if strings.TrimSpace(in.Symptom) == "" {
		return fmt.Errorf("an incident needs a symptom - what did it look like before you knew the cause?")
	}
	if in.TS == "" {
		in.TS = time.Now().UTC().Format(time.RFC3339)
	}
	if in.ID == "" {
		in.ID = incidentID(in.TS, in.Symptom)
	}
	// A resolves id that matches nothing is always a typo, and it would fail
	// SILENTLY - the entry lands, the target stays open, and the count nobody
	// re-derives keeps saying so. Check before writing, and name the misses.
	if len(in.Resolves) > 0 {
		existing, err := ReadIncidents(projectDir, name)
		if err != nil {
			return err
		}
		known := make(map[string]bool, len(existing))
		for _, e := range existing {
			known[e.ID] = true
		}
		var missing []string
		for _, id := range in.Resolves {
			if id = strings.TrimSpace(id); id != "" && !known[id] {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("resolves names %d entr%s this journal does not have: %s (list ids with `pm journal show %s`)",
				len(missing), map[bool]string{true: "y", false: "ies"}[len(missing) == 1], strings.Join(missing, ", "), name)
		}
	}
	if err := os.MkdirAll(journalDir(projectDir), 0755); err != nil {
		return err
	}
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(JournalFilePath(projectDir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// ReadIncidents returns a subject's incidents, oldest first. A journal that has
// never been written to is empty, not an error - a freshly declared subject is
// a normal state. Unparseable lines are skipped so one corrupt line never hides
// the rest of the history (same contract as ReadJournal).
func ReadIncidents(projectDir, name string) ([]Incident, error) {
	if err := ValidateJournalName(name); err != nil {
		return nil, err
	}
	f, err := os.Open(JournalFilePath(projectDir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Incident
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var in Incident
		if json.Unmarshal(sc.Bytes(), &in) == nil && in.Symptom != "" {
			// Lines written before the id field existed get theirs computed
			// here, from the same inputs a write would have used - so history
			// becomes referenceable without rewriting an append-only file.
			if in.ID == "" {
				in.ID = incidentID(in.TS, in.Symptom)
			}
			out = append(out, in)
		}
	}
	return out, sc.Err()
}

// JournalCount is the cheap rollup behind the one-line pointer that pm_context
// and `pm context` print: enough to notice that something is accumulating,
// never enough to be worth reading there instead of in `pm journal`.
type JournalCount struct {
	Name    string `json:"name"`
	Subject string `json:"subject,omitempty"`
	Total   int    `json:"total"`
	Open    int    `json:"open"`
}

// JournalCounts returns one JournalCount per subject declared in project.yaml,
// in declaration order. A declared-but-never-written journal is reported with
// zeroes rather than dropped: "declared and empty" and "not declared" are
// different things, and only the config says which subjects exist.
func JournalCounts(projectDir string, p *Project) ([]JournalCount, error) {
	if p == nil || len(p.Journals) == 0 {
		return nil, nil
	}
	out := make([]JournalCount, 0, len(p.Journals))
	for _, s := range p.Journals {
		c := JournalCount{Name: s.Name, Subject: s.Subject}
		incidents, err := ReadIncidents(projectDir, s.Name)
		if err != nil {
			return nil, err
		}
		resolved := ResolvedIDs(incidents)
		for _, in := range incidents {
			c.Total++
			if StillOpen(in, resolved) {
				c.Open++
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// TagCount is one row of the tag histogram in JournalStats.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
	Open  int    `json:"open"`
}

// MonthCount is one row of the per-month histogram in JournalStats.
type MonthCount struct {
	Month string `json:"month"` // YYYY-MM
	Count int    `json:"count"`
}

// JournalStats is what turns a pile of entries into an argument for a fix:
// how often, how expensive, which causes cluster, and what is still open.
type JournalStats struct {
	Total    int          `json:"total"`
	Open     int          `json:"open"`
	CostMin  int          `json:"cost_min"`
	Costed   int          `json:"costed"` // incidents that recorded a cost (the CostMin average's denominator)
	First    string       `json:"first,omitempty"`
	Last     string       `json:"last,omitempty"`
	Tags     []TagCount   `json:"tags,omitempty"`
	Months   []MonthCount `json:"months,omitempty"`
	OpenList []Incident   `json:"open_list,omitempty"`
	Undated  int          `json:"undated,omitempty"` // entries whose ts did not parse
	// ClosedLater = entries that arrived open and were closed by a LATER entry
	// (a review, typically). Reported separately from Open so a review's effect
	// is visible: otherwise five incidents resolved last week simply vanish
	// from the open count with nothing saying they were ever dealt with.
	ClosedLater int `json:"closed_later,omitempty"`
}

// AggregateIncidents is the pure rollup behind `pm journal stats`, kept free of
// I/O so the reading of the numbers can be tested directly.
//
// Ordering is deliberate: tags by count descending then name, so the cluster
// that most argues for a fix is the first thing on screen; months
// chronologically, because the question there is "is this getting better or
// worse". Open incidents come out newest first - the freshest one is the one
// still fresh enough to act on.
func AggregateIncidents(incidents []Incident) JournalStats {
	st := JournalStats{}
	tags := map[string]*TagCount{}
	months := map[string]int{}
	var first, last time.Time
	// Resolution is a property of the WHOLE journal, not of one entry: a later
	// entry closes an earlier one. Collect it once, up front.
	resolved := ResolvedIDs(incidents)

	for _, in := range incidents {
		st.Total++
		open := StillOpen(in, resolved)
		if open {
			st.Open++
			st.OpenList = append(st.OpenList, in)
		}
		if resolved[in.ID] && in.Open() {
			st.ClosedLater++
		}
		if in.CostMin > 0 {
			st.CostMin += in.CostMin
			st.Costed++
		}
		// Range and buckets go through the PARSED time, never the raw string:
		// RFC3339 permits a numeric offset, so "2026-08-03T10:00:00+02:00"
		// compares lexicographically after a later "...Z" instant.
		if when, ok := in.When(); ok {
			months[when.Format("2006-01")]++
			if first.IsZero() || when.Before(first) {
				first, st.First = when, in.TS
			}
			if when.After(last) {
				last, st.Last = when, in.TS
			}
		} else {
			st.Undated++
		}
		for _, tag := range in.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			tc, ok := tags[tag]
			if !ok {
				tc = &TagCount{Tag: tag}
				tags[tag] = tc
			}
			tc.Count++
			if open {
				tc.Open++
			}
		}
	}

	for _, tc := range tags {
		st.Tags = append(st.Tags, *tc)
	}
	sort.Slice(st.Tags, func(i, j int) bool {
		if st.Tags[i].Count != st.Tags[j].Count {
			return st.Tags[i].Count > st.Tags[j].Count
		}
		return st.Tags[i].Tag < st.Tags[j].Tag
	})

	for m, n := range months {
		st.Months = append(st.Months, MonthCount{Month: m, Count: n})
	}
	sort.Slice(st.Months, func(i, j int) bool { return st.Months[i].Month < st.Months[j].Month })

	// Newest first, undated LAST: an entry with no usable date carries the
	// least to act on, and letting it sort by raw string put it above every
	// real incident.
	sort.SliceStable(st.OpenList, func(i, j int) bool {
		a, aOK := st.OpenList[i].When()
		b, bOK := st.OpenList[j].When()
		if aOK != bOK {
			return aOK
		}
		if !aOK {
			return false // both undated: keep file order
		}
		return a.After(b)
	})
	return st
}

// Journal returns the declared subject with this name, or nil.
func (p *Project) Journal(name string) *JournalSubject {
	if p == nil {
		return nil
	}
	for i := range p.Journals {
		if p.Journals[i].Name == name {
			return &p.Journals[i]
		}
	}
	return nil
}

// JournalNames returns the declared journal names in declaration order.
func (p *Project) JournalNames() []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.Journals))
	for _, s := range p.Journals {
		out = append(out, s.Name)
	}
	return out
}
