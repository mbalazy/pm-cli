package storage

import (
	"fmt"
	"sort"
	"time"
)

// The runs list: one row per tracker, across every project, answering "what is
// going on" at a glance - the screen pm did not have. Until now a run-state was
// only visible by opening the one task it belonged to in the board, which works
// for one run and not at all for several batches on two machines.
//
// THE AGGREGATION LIVES HERE, not in internal/cmd, because two surfaces display
// it: `pm runs` and the board's Runs view. One counting rule, two displays -
// the pattern BuildTrackers already follows for MCP, CLI and TUI. The cell
// String methods are here for the same reason: the vocabulary a user reads
// ("running 3/8", "stale") must not differ between the table and the board.
//
// A row is a TRACKER, deliberately. Runs of standalone tasks (`pm work` on a
// task with no children) are not listed: the screen exists for batches, whose
// progress is exactly what a single task's board card already shows well.

// RUN column vocabulary. A run-state is a file that outlives its process, so
// "running" is a claim that has to be checked (RunState.IsLive) rather than
// believed - RunCellStale is what a crashed manager looks like.
const (
	RunCellPrepped = "prepped" // the tracker exists, nothing has run it yet
	RunCellRunning = "running"
	RunCellDone    = "done"
	RunCellFailed  = "failed"
	RunCellStale   = "stale" // marked running, but its process is gone
)

// ACCEPTANCE column vocabulary. RunCellStale appears here too, for the same
// reason it does in the RUN column: an acceptance killed with SIGKILL leaves
// `status: running` behind forever, and rendering that as "running" would
// promise a run that is not there.
const (
	AcceptCellRunning = "running"
	AcceptCellDone    = "done"
	AcceptCellFailed  = "failed"
	AcceptCellStale   = RunCellStale
)

// RunRow is one tracker's line in the runs list: what the run did, and what its
// acceptance (odbiór) did.
type RunRow struct {
	// Remote is the name of the machine this row came from, empty for a local
	// row. Kept separate from Project (rather than pre-joined into
	// "runner/orbit") so the board can style the two halves and a script
	// can filter on either: the display joins them, the data does not.
	Remote  string `json:"remote,omitempty"`
	Project string `json:"project"`
	Tracker string `json:"tracker,omitempty"`
	Title   string `json:"title,omitempty"`
	// Status is the tracker task's own board status, not the run's.
	Status string `json:"status,omitempty"`
	// Updated is the most recent activity stamp among the tracker task, its run
	// and its acceptance, carried RAW (task stamps are dates, run-states are
	// RFC3339). It is what SortRunRows orders on, so a remote row - whose
	// stamps were resolved on the machine that owns them - merges into one
	// correctly ordered list.
	Updated string `json:"updated,omitempty"`

	Run    RunCell    `json:"run"`
	Accept AcceptCell `json:"acceptance"`

	// Note marks a PLACEHOLDER row: a remote runner that could not be reached
	// or could not answer. Such a row carries no tracker - the whole point is
	// that a sleeping VPS costs one line, never the local rows and never a
	// non-zero exit.
	Note string `json:"note,omitempty"`
}

// RunCell is the RUN column: the state of the run itself plus how many of its
// subs are settled.
type RunCell struct {
	State string `json:"state,omitempty"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// String renders the cell in the vocabulary the column is defined in: counts
// ride along with running/done, and the terminal-or-broken words stand alone.
func (c RunCell) String() string {
	switch c.State {
	case "":
		return "-"
	case RunCellRunning, RunCellDone:
		return fmt.Sprintf("%s %d/%d", c.State, c.Done, c.Total)
	default:
		return c.State
	}
}

// AcceptCell is the ACCEPTANCE column: the state of the odbiór of this run.
type AcceptCell struct {
	State string `json:"state,omitempty"`
	// Host is the machine holding the acceptance claim, when one is live. It is
	// the claim's own field rather than something derived here: the acceptance
	// of a run may happen on a different machine than the run (the whole reason
	// the claim does not trust pids), so "which host" is information only the
	// claim carries.
	Host string `json:"host,omitempty"`
	// Age is how long the acceptance has been going, formatted by
	// FinishClaimAge so the table and `pm finish status` never disagree.
	Age string `json:"age,omitempty"`
	// VisualClaimsOpen is the sum of visual_claims_open across the acceptance's
	// subs - claims that could only be settled by looking at a screen, which a
	// detached acceptance never does. REQUIRED reading, not decoration: without
	// it a batch whose every visual check is still pending reports itself as a
	// plain "done", and this number is the user's morning TODO list.
	VisualClaimsOpen int `json:"visual_claims_open,omitempty"`
}

// String renders the acceptance cell, with the open visual claims appended
// wherever there are any - including on a "done", which is precisely the case
// the count exists for.
func (c AcceptCell) String() string {
	if c.State == "" {
		return "-"
	}
	s := c.State
	switch {
	case c.Host != "" && c.Age != "":
		s += fmt.Sprintf(" (%s, %s)", c.Host, c.Age)
	case c.Host != "":
		s += fmt.Sprintf(" (%s)", c.Host)
	case c.Age != "":
		s += fmt.Sprintf(" (%s)", c.Age)
	}
	if c.VisualClaimsOpen > 0 {
		s += fmt.Sprintf(", %d visual claim(s) open", c.VisualClaimsOpen)
	}
	return s
}

// LocalRunRows builds the runs rows for the projects on THIS machine, newest
// activity first. An empty projects list means every active project - the
// default, because the screen's reason for existing is that the user has
// batches in more than one place.
//
// Reads only: no run-state, claim or task is written. A project whose tasks
// cannot be read fails the call rather than being silently dropped, since that
// is a broken local store rather than the unreachable-machine case the Note
// rows exist for.
func LocalRunRows(store TaskStore, projects []string) ([]RunRow, error) {
	if len(projects) == 0 {
		all, err := store.ListActiveProjects()
		if err != nil {
			return nil, err
		}
		projects = all
	}

	var rows []RunRow
	for _, slug := range projects {
		tasks, err := store.GetTasks(slug)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", slug, err)
		}
		trackers, _ := BuildTrackers(tasks, store.GetLandingStatuses(slug)...)
		if len(trackers) == 0 {
			continue
		}
		byID := make(map[string]*Task, len(tasks))
		for _, t := range tasks {
			byID[t.Meta.ID] = t
		}
		dir := store.ProjectDir(slug)
		runs := ReadRunStates(dir)
		accepts := ReadFinishRunStates(dir)
		for _, tr := range trackers {
			rows = append(rows, runRow(slug, dir, tr, byID[tr.ID], runs[tr.ID], accepts[tr.ID]))
		}
	}
	SortRunRows(rows)
	return rows, nil
}

// runRow assembles one tracker's row. dir is the project dir, needed for the
// acceptance claim - the one input that is neither a task nor a run-state.
func runRow(slug, dir string, tr Tracker, task *Task, run, accept *RunState) RunRow {
	row := RunRow{
		Project: slug,
		Tracker: tr.ID,
		Title:   tr.Title,
		Status:  tr.Status,
		Run:     runCell(tr, run),
		Accept:  acceptCell(dir, tr.ID, accept),
	}
	stamps := []string{}
	if task != nil {
		stamps = append(stamps, task.Meta.Updated)
	}
	if run != nil {
		stamps = append(stamps, run.Updated)
	}
	if accept != nil {
		stamps = append(stamps, accept.Updated)
	}
	row.Updated = latestStamp(stamps)
	return row
}

// runCell derives the RUN column from the run-state.
//
// The denominator is the number of subs the RUN drove (a re-run seeds the ones
// it will skip too), falling back to the tracker's child count when a run-state
// carries no subs at all - a run killed before it seeded them. The numerator
// counts subs the run is FINISHED with, whatever the outcome: merged, pushed,
// failed, skipped and manual are all settled, and only pending/running are not.
// Counting green subs instead would make a batch that parked two subs on
// waiting sit at "running 6/8" after the manager had exited.
func runCell(tr Tracker, st *RunState) RunCell {
	if st == nil {
		return RunCell{State: RunCellPrepped}
	}
	cell := RunCell{Total: len(st.Subs)}
	if cell.Total == 0 {
		cell.Total = tr.Total
	}
	for _, s := range st.Subs {
		switch s.Status {
		case "", "pending", RunStatusRunning:
		default:
			cell.Done++
		}
	}
	switch st.Status {
	case RunStatusRunning:
		cell.State = RunCellRunning
		if !st.IsLive() {
			cell.State = RunCellStale
		}
	case RunStatusFailed:
		cell.State = RunCellFailed
	case RunStatusDone:
		cell.State = RunCellDone
	default:
		// A run-state whose status word nothing in pm writes: report it rather
		// than mapping it onto one of ours.
		cell.State = st.Status
	}
	return cell
}

// acceptCell derives the ACCEPTANCE column from the acceptance run-state and
// the claim.
//
// A LIVE CLAIM WINS over the run-state, and that ordering is the point: the
// acceptance may be running on another machine, where its run-state's pid means
// nothing here, and the claim is the one artifact designed to be read across
// hosts. Only when no claim is live does the run-state speak - and then a
// `running` it cannot back up reads as stale.
//
// The open visual claims are carried whatever the state word, because they
// outlive the run that counted them: they are unfinished work on the tracker,
// true until somebody accepts it again.
func acceptCell(dir, trackerID string, st *RunState) AcceptCell {
	cell := AcceptCell{}
	if st != nil {
		for _, s := range st.Subs {
			if s.VisualClaimsOpen > 0 {
				cell.VisualClaimsOpen += s.VisualClaimsOpen
			}
		}
	}
	if holder := LiveFinishClaimHolder(dir, trackerID); holder != nil {
		cell.State = AcceptCellRunning
		cell.Host = holder.Host
		cell.Age = FinishClaimAge(holder.Started)
		return cell
	}
	if st == nil {
		return cell
	}
	switch st.Status {
	case RunStatusRunning:
		if st.IsLive() {
			cell.State = AcceptCellRunning
			cell.Age = FinishClaimAge(st.Started)
		} else {
			cell.State = AcceptCellStale
		}
	case RunStatusFailed:
		cell.State = AcceptCellFailed
	case RunStatusDone:
		cell.State = AcceptCellDone
	default:
		cell.State = st.Status
	}
	return cell
}

// SortRunRows orders rows newest activity first. Exported because the board
// merges local and remote rows itself and must land on the same order as the
// CLI. Rows whose stamp cannot be parsed - a hand-edited task, and every Note
// placeholder, which has no activity at all - sort LAST rather than first, the
// same rule the journal follows for an unparsable ts. Ties break on
// remote/project/tracker so the order is total and a test can rely on it.
func SortRunRows(rows []RunRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, aok := parseRunStamp(rows[i].Updated)
		b, bok := parseRunStamp(rows[j].Updated)
		if aok != bok {
			return aok
		}
		if aok && !a.Equal(b) {
			return a.After(b)
		}
		if rows[i].Remote != rows[j].Remote {
			return rows[i].Remote < rows[j].Remote
		}
		if rows[i].Project != rows[j].Project {
			return rows[i].Project < rows[j].Project
		}
		return rows[i].Tracker < rows[j].Tracker
	})
}

// latestStamp returns the most recent of the given stamps, raw. Mixed formats
// are expected: a task's `updated` is a date (storage.Today), a run-state's is
// RFC3339, and the newest of the two is a real question.
func latestStamp(stamps []string) string {
	var best string
	var bestAt time.Time
	for _, s := range stamps {
		at, ok := parseRunStamp(s)
		if !ok {
			continue
		}
		if best == "" || at.After(bestAt) {
			best, bestAt = s, at
		}
	}
	return best
}

// parseRunStamp parses the stamp formats pm writes: RFC3339 for anything
// machine-written, a bare date for a task's `updated`.
func parseRunStamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if at, err := time.Parse(layout, s); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}
