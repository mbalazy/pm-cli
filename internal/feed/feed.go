// Package feed is the cockpit's change feed: "what changed since yesterday
// 18:00", assembled from SOURCES behind one interface (pm's own files, git,
// GitHub, later Slack and the rest) and cached on disk so that `pm today`
// and a restarted `pm serve` never need the network to answer.
//
// This is the first code in pm that leaves pm's own files - the git and
// github sources run `git` and `gh` inside the projects' checkouts - so the
// rules are strict: every source has a switch in the cockpit config, every
// external command has a timeout, a failing source is reported per source
// and never stops the others, and a refresh runs only inside the configured
// window unless a human asks for one.
//
// A new source is a new file implementing Source plus one line in the
// registry (Sources); nothing else knows the list.
package feed

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// Severity vocabulary of an event: the attention aggregation's, so a feed
// row and a queue row read alike.
const (
	SeverityCrit = storage.SeverityCrit
	SeverityWarn = storage.SeverityWarn
	SeverityInfo = storage.SeverityInfo
	SeverityOK   = storage.SeverityOK
)

// Event is one thing that happened. ID is STABLE across fetches (the same
// commit, comment or status change yields the same id every time), which
// is what makes a refresh idempotent: the cache deduplicates on it.
type Event struct {
	ID       string `json:"id"`
	TS       string `json:"ts"` // RFC3339
	Source   string `json:"source"`
	Project  string `json:"project"`
	Group    string `json:"group"`
	TaskID   string `json:"task_id,omitempty"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	URL      string `json:"url,omitempty"`
	Severity string `json:"severity"`
	// Seen is set by the reader (an event older than the seen stamp), never
	// stored.
	Seen bool `json:"seen"`
}

// Project is what a source gets per active project: identity, group, the
// checkout path (empty when the project has none), the tasks, and the
// Slack mapping (project.yaml `slack`; nil = none).
type Project struct {
	Slug  string
	Group string
	Path  string
	Dir   string // the pm project dir (.executor/ lives under it)
	Tasks []*storage.Task
	Slack *storage.SlackConfig
	// GHAccount is project.yaml's gh_account: the gh account whose token
	// the git and github sources hand gh (see githubContext).
	GHAccount string
}

// Source is the one interface every feed source implements. Fetch returns
// the events in [from, to) for the given projects; an error covers the
// source as a whole (its status carries it), while a per-project problem
// is returned as a *ProjectError so the other projects still count.
type Source interface {
	Name() string
	Fetch(ctx context.Context, from, to time.Time, projects []Project) ([]Event, error)
}

// Configurable is implemented by a source that takes settings from the
// cockpit block (the git source's all_branches, the slack source's server
// list). Refresh hands every Configurable source the CURRENT block before
// its Fetch, so an edit in the settings screen takes effect on the next
// refresh without a restart - the same per-call rule the cutoff and the
// window already follow.
type Configurable interface {
	Configure(cfg *storage.CockpitConfig)
}

// PRSnapshotter is implemented by a source that knows the CURRENT state of
// the PRs it listed (the git source). Events are history - "a reviewer tests the
// preview" stays true of 11:20 after the PR merged at 14:00 - so a reader
// that describes the present (the report) needs the state beside them.
type PRSnapshotter interface {
	PRStates() []PRState
}

// PRState is one PR as of the last refresh that listed it.
type PRState struct {
	Project string `json:"project"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	// State is open, merged or closed.
	State          string `json:"state"`
	Draft          bool   `json:"draft,omitempty"`
	ReviewDecision string `json:"review_decision,omitempty"`
	UpdatedAt      string `json:"updated_at"`
	TaskID         string `json:"task_id,omitempty"`
}

// ProjectError is a source's failure on ONE project (no checkout, no gh);
// Fetch may return it wrapped in errors.Join with the events of the others.
type ProjectError struct {
	Project string
	Err     error
}

func (e *ProjectError) Error() string { return e.Project + ": " + e.Err.Error() }
func (e *ProjectError) Unwrap() error { return e.Err }

// SourceStatus is what /api/changes reports per source.
type SourceStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// LastFetch is when the source last ran (RFC3339), empty = never.
	LastFetch string `json:"last_fetch,omitempty"`
	// Error is the last run's error text, empty = clean.
	Error string `json:"error,omitempty"`
	// Events is how many events the last run produced.
	Events int `json:"events"`
}

// state is the persisted feed state beside the cache: source statuses and
// the seen stamp.
type state struct {
	Sources []SourceStatus `json:"sources"`
	Seen    string         `json:"seen,omitempty"`
}

// Feed owns the cache directory and the sources. One per process; safe for
// concurrent use (the server's scheduler and a manual refresh may race).
//
// Two locks on purpose: refreshMu serialises refreshes (one fetch round at
// a time), mu guards the CACHE FILES only. A refresh runs git, gh and slack
// for up to minutes; holding mu across that would make every Read (the
// Changes screen, the report writer) and MarkSeen wait for the network,
// which is what happened before the split.
type Feed struct {
	root      string
	sources   []Source
	refreshMu sync.Mutex
	mu        sync.Mutex
}

// CacheDir is the feed's directory under the pm root.
func CacheDir(root string) string { return filepath.Join(root, ".cockpit", "changes") }

// New builds a feed over the pm root with the given sources (nil = the
// default registry, Sources with DefaultRunner).
func New(root string, sources []Source) *Feed {
	if sources == nil {
		sources = Sources(root, DefaultRunner)
	}
	return &Feed{root: root, sources: sources}
}

// Sources is the registry: every source pm ships, in the order the feed
// runs them. A new source is appended here and nowhere else. root is the
// pm root (the pm source reads focus.yaml there); run executes git/gh.
func Sources(root string, run Runner) []Source {
	return []Source{
		&PMSource{Root: root},
		&GitSource{Run: run},
		&GitHubSource{Run: run},
		&SlackSource{},
	}
}

// Result is what one refresh reports.
type Result struct {
	// Sources are the statuses after this run (every registered source,
	// disabled ones included).
	Sources []SourceStatus `json:"sources"`
	// Added is how many NEW events the run cached (after dedup).
	Added int `json:"added"`
	// From/To bound the fetch.
	From string `json:"from"`
	To   string `json:"to"`
}

// Refresh runs every enabled source over the active projects for the window
// [from, now) and caches the events. The cutoff is the only input a caller
// decides; the projects come from the store. A source that fails is
// recorded in its status and the others still run - the ONE guarantee this
// package makes to the user about the network. The state file is written
// whatever happened, so a failure is visible on the next read.
func (f *Feed) Refresh(ctx context.Context, store storage.TaskStore, cfg *storage.CockpitConfig, from, now time.Time) (*Result, error) {
	f.refreshMu.Lock()
	defer f.refreshMu.Unlock()
	if cfg == nil {
		c := storage.DefaultCockpitConfig()
		cfg = &c
	}

	projects, err := activeProjects(store)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	st := f.readState()
	f.mu.Unlock()
	statusIdx := map[string]int{}
	for i, s := range st.Sources {
		statusIdx[s.Name] = i
	}
	setStatus := func(s SourceStatus) {
		if i, ok := statusIdx[s.Name]; ok {
			st.Sources[i] = s
			return
		}
		statusIdx[s.Name] = len(st.Sources)
		st.Sources = append(st.Sources, s)
	}

	var fresh []Event
	var snap []PRState
	snapped := false
	for _, src := range f.sources {
		name := src.Name()
		status := SourceStatus{Name: name, Enabled: cfg.SourceEnabled(name)}
		if prev, ok := statusIdx[name]; ok {
			status.LastFetch = st.Sources[prev].LastFetch
		}
		if !status.Enabled {
			setStatus(status)
			continue
		}
		if c, ok := src.(Configurable); ok {
			c.Configure(cfg)
		}
		events, ferr := src.Fetch(ctx, from, now, projects)
		if ps, ok := src.(PRSnapshotter); ok {
			snap, snapped = append(snap, ps.PRStates()...), true
		}
		status.LastFetch = now.Format(time.RFC3339)
		if ferr != nil {
			status.Error = ferr.Error()
		}
		// A source windows its own query, but the cache holds ONLY the
		// window: an event a source hands back from before the cutoff is
		// not a change since the cutoff, whatever the source thinks.
		kept := 0
		for _, e := range events {
			if _, ok := inWindow(e.TS, from, now); !ok {
				continue
			}
			if e.Source == "" {
				e.Source = name
			}
			fresh = append(fresh, e)
			kept++
		}
		status.Events = kept
		setStatus(status)
	}
	// Keep the registry's order in the file so the UI's source list is stable.
	sort.SliceStable(st.Sources, func(i, j int) bool {
		return sourceRank(f.sources, st.Sources[i].Name) < sourceRank(f.sources, st.Sources[j].Name)
	})

	// The cache lock is taken only now, around the file writes. The state
	// is re-read under it: a MarkSeen that landed while the sources were
	// running must survive - this run owns the source statuses, nothing
	// else in the file.
	f.mu.Lock()
	defer f.mu.Unlock()
	added, err := f.appendEvents(fresh, now)
	if err != nil {
		return nil, err
	}
	f.rotate(now)
	if snapped {
		if err := f.mergePRStates(snap, now); err != nil {
			return nil, err
		}
	}
	cur := f.readState()
	cur.Sources = st.Sources
	if err := f.writeState(cur); err != nil {
		return nil, err
	}
	return &Result{Sources: cur.Sources, Added: added, From: from.Format(time.RFC3339), To: now.Format(time.RFC3339)}, nil
}

func sourceRank(sources []Source, name string) int {
	for i, s := range sources {
		if s.Name() == name {
			return i
		}
	}
	return len(sources)
}

// Changes is what /api/changes answers: the events since the cutoff, each
// flagged seen or not, the source statuses and the marks.
type Changes struct {
	// Cutoff is the window's start (RFC3339); Events are those at or after it.
	Cutoff string  `json:"cutoff"`
	Events []Event `json:"events"`
	// Unseen counts the events newer than the seen stamp.
	Unseen  int            `json:"unseen"`
	Seen    string         `json:"seen,omitempty"`
	Sources []SourceStatus `json:"sources"`
}

// Read returns the cached events at or after cutoff, newest first, with the
// seen flag applied. Reads only the cache; never fetches.
func (f *Feed) Read(cutoff time.Time) (*Changes, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.readState()
	events, err := f.readEvents(cutoff)
	if err != nil {
		return nil, err
	}
	seenAt, hasSeen := storage.ParseStamp(st.Seen)
	out := &Changes{Cutoff: cutoff.Format(time.RFC3339), Events: []Event{}, Seen: st.Seen, Sources: st.Sources}
	if out.Sources == nil {
		out.Sources = []SourceStatus{}
	}
	for _, e := range events {
		if ts, ok := storage.ParseStamp(e.TS); ok && hasSeen && !ts.After(seenAt) {
			e.Seen = true
		} else {
			out.Unseen++
		}
		out.Events = append(out.Events, e)
	}
	return out, nil
}

// MarkSeen records that everything up to at (inclusive) has been read.
func (f *Feed) MarkSeen(at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.readState()
	st.Seen = at.Format(time.RFC3339)
	return f.writeState(st)
}

// Digest shapes the cached feed for the attention aggregation's `changes`
// section: the unseen count and the newest events as rows. Cache only.
func (f *Feed) Digest(cutoff time.Time, rows int) (*storage.ChangesDigest, error) {
	ch, err := f.Read(cutoff)
	if err != nil {
		return nil, err
	}
	// No cache and never fetched = no feed yet, which the section reports
	// with its note rather than as "0 changes".
	if len(ch.Sources) == 0 {
		return nil, nil
	}
	d := &storage.ChangesDigest{Count: ch.Unseen}
	for _, e := range ch.Events {
		if len(d.Rows) >= rows {
			break
		}
		if e.Seen {
			continue
		}
		d.Rows = append(d.Rows, EventRow(e))
	}
	return d, nil
}

// ReadDigest is Digest over the cache alone, for a reader that runs no
// source (the attention aggregation, `pm today`).
func ReadDigest(root string, cutoff time.Time, rows int) (*storage.ChangesDigest, error) {
	return New(root, []Source{}).Digest(cutoff, rows)
}

// EventRow renders an event as an attention row.
func EventRow(e Event) storage.AttentionRow {
	r := storage.AttentionRow{
		Section: storage.SectionChanges, Severity: e.Severity, Project: e.Project, Group: e.Group,
		TaskID: e.TaskID, Title: e.Title, Reason: e.Source + ": " + e.Detail,
		Actions: []string{storage.ActionMarkSeen},
	}
	if e.Detail == "" {
		r.Reason = e.Source
	}
	if e.TaskID != "" {
		r.Actions = append(r.Actions, storage.ActionOpen)
	}
	if e.URL != "" {
		r.Actions = append(r.Actions, storage.ActionOpenPR)
	}
	if r.Severity == "" {
		r.Severity = SeverityInfo
	}
	if ts, ok := storage.ParseStamp(e.TS); ok {
		secs := int64(time.Since(ts) / time.Second)
		if secs < 0 {
			secs = 0
		}
		r.Since, r.AgeSeconds = e.TS, &secs
	}
	return r
}

// --- cache ---

const (
	stateFile   = "state.json"
	keepDays    = 14
	dayLayout   = "2006-01-02"
	jsonlSuffix = ".jsonl"
)

func (f *Feed) dir() string { return CacheDir(f.root) }

func (f *Feed) readState() state {
	var st state
	data, err := os.ReadFile(filepath.Join(f.dir(), stateFile))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	return st
}

func (f *Feed) writeState(st state) error {
	if err := os.MkdirAll(f.dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(f.dir(), stateFile), data)
}

// prsFile holds the PR snapshot: the newest known state per PR.
const prsFile = "prs.json"

func (f *Feed) readPRStates() []PRState {
	var rows []PRState
	data, err := os.ReadFile(filepath.Join(f.dir(), prsFile))
	if err != nil {
		return nil
	}
	_ = json.Unmarshal(data, &rows)
	return rows
}

// mergePRStates writes the fresh rows over the stored ones, PR by PR. A PR
// the fresh listing lacks keeps its older row (its project's gh failed this
// time, or it went quiet); rows not updated for keepDays are dropped.
func (f *Feed) mergePRStates(fresh []PRState, now time.Time) error {
	key := func(r PRState) string { return r.Project + "#" + fmt.Sprint(r.Number) }
	seen := map[string]bool{}
	rows := append([]PRState{}, fresh...)
	for _, r := range fresh {
		seen[key(r)] = true
	}
	horizon := now.AddDate(0, 0, -keepDays)
	for _, r := range f.readPRStates() {
		if seen[key(r)] {
			continue
		}
		if ts, ok := storage.ParseStamp(r.UpdatedAt); ok && ts.Before(horizon) {
			continue
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool { return key(rows[i]) < key(rows[j]) })
	if err := os.MkdirAll(f.dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(f.dir(), prsFile), data)
}

// PRStates returns the snapshot's PRs updated at or after since: the
// current state of every PR the feed's window can mention.
func (f *Feed) PRStates(since time.Time) []PRState {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []PRState
	for _, r := range f.readPRStates() {
		if ts, ok := storage.ParseStamp(r.UpdatedAt); ok && ts.Before(since) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// appendEvents adds the events the cache does not hold yet, one JSONL file
// per event day (the executor journal's grain). Returns how many were new.
// An event older than the keep window is dropped rather than written into a
// file rotate is about to delete.
func (f *Feed) appendEvents(events []Event, now time.Time) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(f.dir(), 0o755); err != nil {
		return 0, err
	}
	oldest := now.AddDate(0, 0, -keepDays).Format(dayLayout)
	byDay := map[string][]Event{}
	for _, e := range events {
		ts, ok := storage.ParseStamp(e.TS)
		if !ok {
			continue // an event without a time cannot be windowed; dropped
		}
		day := ts.In(time.Local).Format(dayLayout)
		if day < oldest {
			continue
		}
		byDay[day] = append(byDay[day], e)
	}
	added := 0
	for day, evs := range byDay {
		path := filepath.Join(f.dir(), day+jsonlSuffix)
		known := map[string]bool{}
		for _, e := range readJSONL(path) {
			known[e.ID] = true
		}
		fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return added, err
		}
		w := bufio.NewWriter(fh)
		for _, e := range evs {
			if known[e.ID] {
				continue
			}
			known[e.ID] = true
			e.Seen = false
			line, err := json.Marshal(e)
			if err != nil {
				continue
			}
			w.Write(line)
			w.WriteByte('\n')
			added++
		}
		if err := w.Flush(); err != nil {
			fh.Close()
			return added, err
		}
		if err := fh.Close(); err != nil {
			return added, err
		}
	}
	return added, nil
}

// readEvents returns the cached events at or after cutoff, newest first.
func (f *Feed) readEvents(cutoff time.Time) ([]Event, error) {
	entries, err := os.ReadDir(f.dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cutoffDay := cutoff.In(time.Local).Format(dayLayout)
	var out []Event
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, jsonlSuffix) {
			continue
		}
		day := strings.TrimSuffix(name, jsonlSuffix)
		if day < cutoffDay {
			continue
		}
		for _, ev := range readJSONL(filepath.Join(f.dir(), name)) {
			if ts, ok := storage.ParseStamp(ev.TS); ok && !ts.Before(cutoff) {
				out = append(out, ev)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := storage.ParseStamp(out[i].TS)
		b, _ := storage.ParseStamp(out[j].TS)
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// rotate drops day files older than keepDays. Best-effort.
func (f *Feed) rotate(now time.Time) {
	entries, err := os.ReadDir(f.dir())
	if err != nil {
		return
	}
	oldest := now.AddDate(0, 0, -keepDays).Format(dayLayout)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, jsonlSuffix) {
			continue
		}
		if strings.TrimSuffix(name, jsonlSuffix) < oldest {
			_ = os.Remove(filepath.Join(f.dir(), name))
		}
	}
}

func readJSONL(path string) []Event {
	fh, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer fh.Close()
	var out []Event
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out
}

func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".feed-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// --- helpers shared by sources ---

// activeProjects reads the inputs every source gets. A project whose tasks
// cannot be read fails the refresh: that is a broken store, not a flaky
// network.
func activeProjects(store storage.TaskStore) ([]Project, error) {
	slugs, err := store.ListActiveProjects()
	if err != nil {
		return nil, err
	}
	var out []Project
	for _, slug := range slugs {
		proj, err := store.GetProject(slug)
		if err == nil && proj.Archived {
			continue
		}
		tasks, err := store.GetTasks(slug)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", slug, err)
		}
		p := Project{Slug: slug, Group: proj.GroupSlug(slug), Dir: store.ProjectDir(slug), Tasks: tasks}
		if proj != nil {
			p.Path = proj.Path
			p.Slack = proj.Slack
			p.GHAccount = proj.GHAccount
		}
		out = append(out, p)
	}
	return out, nil
}

// EventID derives a stable id from the parts that identify an event.
func EventID(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:8])
}

// inWindow reports whether ts (a pm stamp) falls in [from, to).
func inWindow(stamp string, from, to time.Time) (time.Time, bool) {
	ts, ok := storage.ParseStamp(stamp)
	if !ok {
		return ts, false
	}
	return ts, !ts.Before(from) && ts.Before(to)
}

// Cutoff is the window's start for the config's cutoff hour: see
// storage.CutoffSince.
func Cutoff(cfg *storage.CockpitConfig, now time.Time) time.Time {
	hour := 18
	if cfg != nil {
		hour = cfg.CutoffHour
	}
	return storage.CutoffSince(hour, now)
}
