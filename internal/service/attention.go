package service

import (
	"sort"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/feed"
	"github.com/mbalazy/pm/internal/storage"
)

// AttentionInput narrows the queue. Both empty = every active project.
type AttentionInput struct {
	Project string `json:"project,omitempty" jsonschema:"Project slug or prefix - only that project's rows"`
	Group   string `json:"group,omitempty" jsonschema:"Group slug - every member project's rows"`
}

// Attention computes the attention queue (storage.BuildAttention) with the
// cockpit config loaded from the store and scopes it to the input. The
// config is loaded once, here: an unparsable config.yaml fails the call the
// way ListGroups fails - the thresholds ARE the definition of the queue,
// and a queue computed on defaults because the file has a typo would be a
// different queue presented as the user's.
func Attention(store storage.TaskStore, in AttentionInput) (*storage.Attention, error) {
	return AttentionAt(store, in, time.Now())
}

// AttentionAt is Attention against an explicit clock: `pm serve` hands in
// its injected Options.Clock, so the queue's ages and the changes digest's
// cutoff agree with the cutoff /api/changes uses on the same request.
func AttentionAt(store storage.TaskStore, in AttentionInput, now time.Time) (*storage.Attention, error) {
	cfg, err := store.LoadConfig()
	if err != nil {
		return nil, err
	}
	project := ""
	if in.Project != "" {
		slug, err := store.ResolveProject(in.Project)
		if err != nil {
			return nil, err
		}
		project = slug
	}
	opts := storage.AttentionOptions{Now: now}
	// The change feed's digest comes off its CACHE - the aggregation never
	// fetches. Best-effort: an unreadable cache is a section without a
	// feed, and the section's note says so.
	if d, err := feed.ReadDigest(store.RootDir(), feed.Cutoff(&cfg.Cockpit, opts.Now), 5); err == nil {
		opts.Changes = d
	}
	a, err := storage.BuildAttention(store, &cfg.Cockpit, opts)
	if err != nil {
		return nil, err
	}
	return a.Scope(project, in.Group), nil
}

// ContextWaitingLimit caps the waiting rows in pm_context's attention block.
const ContextWaitingLimit = 10

// AttentionDigest is the pm_context budget of the queue: what a session
// needs to know it is walking into, not the home screen. needs_me and
// stuck_projects in full (they are short by nature), waiting capped at
// ContextWaitingLimit with the no-reason alarms first, every other section
// as a count. No task bodies; rows carry titles and one-line reasons only.
type AttentionDigest struct {
	// WIP is the header number: doing tasks touched this week.
	WIP int `json:"wip"`
	// NeedsMe is the whole needs_me section, worst first.
	NeedsMe []storage.AttentionRow `json:"needs_me"`
	// Waiting is at most ContextWaitingLimit rows: no_reason first, then
	// oldest first; WaitingTotal says how many there are.
	Waiting      []storage.AttentionRow `json:"waiting"`
	WaitingTotal int                    `json:"waiting_total"`
	// StuckProjects lists the projects meeting the stuck rule.
	StuckProjects []storage.AttentionRow `json:"stuck_projects"`
	// Counts holds the row total of every OTHER enabled section by name.
	Counts map[string]int `json:"counts"`
	// Note names a section that is on but not computed (see storage).
	Note string `json:"note,omitempty"`
}

// DigestAttention reduces a queue to the pm_context budget.
func DigestAttention(a *storage.Attention) *AttentionDigest {
	if a == nil {
		return nil
	}
	d := &AttentionDigest{
		WIP:           a.WIP,
		NeedsMe:       []storage.AttentionRow{},
		Waiting:       []storage.AttentionRow{},
		StuckProjects: []storage.AttentionRow{},
		Counts:        map[string]int{},
	}
	notes := []string{}
	for _, sec := range a.Sections {
		switch sec.Name {
		case storage.SectionNeedsMe:
			d.NeedsMe = append(d.NeedsMe, sec.Rows...)
		case storage.SectionStuckProjects:
			d.StuckProjects = append(d.StuckProjects, sec.Rows...)
		case storage.SectionWaiting:
			d.WaitingTotal = sec.Total
			rows := append([]storage.AttentionRow(nil), sec.Rows...)
			// The section itself is age-ordered; the digest promotes the
			// alarms, because ten rows is all a session sees.
			sort.SliceStable(rows, func(i, j int) bool {
				return hasFlag(rows[i], storage.FlagNoReason) && !hasFlag(rows[j], storage.FlagNoReason)
			})
			if len(rows) > ContextWaitingLimit {
				rows = rows[:ContextWaitingLimit]
			}
			d.Waiting = append(d.Waiting, rows...)
		default:
			d.Counts[sec.Name] = sec.Total
			if sec.Note != "" && sec.Name != storage.SectionChanges {
				notes = append(notes, sec.Name+": "+sec.Note)
			}
		}
	}
	if len(notes) > 0 {
		d.Note = strings.Join(notes, "; ")
	}
	return d
}

func hasFlag(r storage.AttentionRow, flag string) bool {
	for _, f := range r.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// contextAttention is the best-effort attention block for the context
// calls: the rollup is a session-start call and must not fail over a
// cockpit-side problem, so an error becomes a note, never an error.
func contextAttention(store storage.TaskStore, in AttentionInput) (*AttentionDigest, string) {
	a, err := Attention(store, in)
	if err != nil {
		return nil, "attention unavailable: " + err.Error()
	}
	return DigestAttention(a), ""
}
