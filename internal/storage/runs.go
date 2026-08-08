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
// AcceptCellPartial and AcceptCellBlocked are the acceptance's OWN verdict
// words (its result contract is done | partial | blocked), reported here rather
// than mapped onto a run status - see acceptVerdict.
const (
	AcceptCellRunning = "running"
	AcceptCellDone    = "done"
	AcceptCellPartial = "partial"
	AcceptCellBlocked = "blocked"
	AcceptCellFailed  = "failed"
	AcceptCellStale   = RunCellStale
)

// RunRow is one tracker's line in the runs list: what the run did, and what its
// acceptance did.
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

// String renders the cell as "<state> N/M". The counts ride along with EVERY
// state that has any - a crashed batch's "stale 2/8" is exactly what a human
// coming back to it needs, and dropping them there would hide the progress of
// the runs most likely to need attention. Only `prepped` stands alone, because
// there is nothing yet to count.
func (c RunCell) String() string {
	switch {
	case c.State == "":
		return "-"
	case c.State == RunCellPrepped || c.Total == 0:
		return c.State
	default:
		return fmt.Sprintf("%s %d/%d", c.State, c.Done, c.Total)
	}
}

// AcceptCell is the ACCEPTANCE column: the state of the acceptance of this run.
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
		// Read once and reused: GetLandingStatuses re-reads project.yaml on every
		// call, and both consumers below want the same answer.
		landing := store.GetLandingStatuses(slug)
		trackers, _ := BuildTrackers(tasks, landing...)
		if len(trackers) == 0 {
			continue
		}
		byID := make(map[string]*Task, len(tasks))
		// landed answers "has this sub's own task actually finished", which is
		// what tells the run's un-run outcomes from its landed ones (subSettled).
		landed := make(map[string]bool, len(tasks))
		for _, t := range tasks {
			byID[t.Meta.ID] = t
			landed[t.Meta.ID] = terminalStatus(t.Meta.Status, landing)
		}
		dir := store.ProjectDir(slug)
		runs := ReadRunStates(dir)
		accepts := ReadFinishRunStates(dir)
		for _, tr := range trackers {
			rows = append(rows, runRow(slug, dir, tr, byID[tr.ID], runs[tr.ID], accepts[tr.ID], landed))
		}
	}
	SortRunRows(rows)
	return rows, nil
}

// runRow assembles one tracker's row. dir is the project dir, needed for the
// acceptance claim - the one input that is neither a task nor a run-state.
func runRow(slug, dir string, tr Tracker, task *Task, run, accept *RunState, landed map[string]bool) RunRow {
	row := RunRow{
		Project: slug,
		Tracker: tr.ID,
		Title:   tr.Title,
		Status:  tr.Status,
		Run:     runCell(tr, run, landed),
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

// Sub statuses a run-state can carry that are NOT an outcome. Spelled here
// rather than imported from the manager because storage is the layer below it;
// the full vocabulary is documented on SubRun.Status.
const (
	subStatusPending = "pending"
	subStatusSkipped = "skipped"
	subStatusManual  = "manual"
	subStatusAborted = "aborted"
)

// runCell derives the RUN column from the run-state.
//
// The denominator is measured against the TRACKER, not just the run: a run-state
// records the subs that existed when it started, and a tracker that has since
// grown (the normal prep-iterate-rerun flow) would otherwise report "done 3/3"
// with five children nobody has touched. The larger of the two counts is the
// honest one - a run that drove subs no longer among the children still drove
// them.
//
// The numerator counts subs the run is FINISHED with. Failed and blocked count:
// the run is done with them, and counting only green subs would leave a batch
// that parked two subs on waiting sitting at "running 6/8" after the manager had
// exited. Pending and running plainly do not count. `skipped` and `manual` are
// the interesting ones - see subSettled.
func runCell(tr Tracker, st *RunState, landed map[string]bool) RunCell {
	if st == nil {
		return RunCell{State: RunCellPrepped}
	}
	cell := RunCell{Total: len(st.Subs)}
	if tr.Total > cell.Total {
		cell.Total = tr.Total
	}
	for _, s := range st.Subs {
		if subSettled(s, landed) {
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
		// than mapping it onto one of ours. An EMPTY one still has to say
		// something - a truncated or hand-edited file is not the same as having
		// no run, and RunCell.String renders the empty state as "-", which is
		// how "no acceptance" is spelled.
		cell.State = st.Status
		if cell.State == "" {
			cell.State = "unknown"
		}
	}
	return cell
}

// subSettled reports whether the run reached an outcome for this sub.
//
// `skipped`, `manual` and `aborted` cannot be judged from the run alone, because
// the manager writes each of them for situations that mean opposite things:
// `skipped` means "already landed before this run" (progress), but ALSO "its
// depends_on is unmet", "not ready" and "never started - the run aborted"
// (nothing); `manual` is a permanent human gate pm never runs at all; and
// `aborted` is a sub that hit an account wall and was MOVED BACK to its ready
// status for a re-run to pick up. The sub's OWN task status settles all three: a
// sub that landed sits on a terminal status, and one waiting for a dependency, a
// human or a fresh quota does not. Counting them regardless would let a batch
// that landed 2 of 8 and parked six on a failed dependency print "done 8/8" -
// the single most misleading thing this screen could say.
func subSettled(s SubRun, landed map[string]bool) bool {
	switch s.Status {
	case "", subStatusPending, RunStatusRunning:
		return false
	case subStatusSkipped, subStatusManual, subStatusAborted:
		return landed[s.ID]
	default:
		return true
	}
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
		if v := acceptVerdict(st); v != "" {
			cell.State = v
		}
	default:
		cell.State = st.Status
	}
	return cell
}

// acceptVerdict returns the acceptance's own verdict on the run it accepted.
//
// An acceptance run-state's top-level Status says only whether the WORKER came
// back: `pm finish` stamps RunStatusDone for any worker that returned at all and
// RunStatusFailed only when one died. The verdict - done | partial | blocked,
// the acceptance's result contract - lives on its single sub. Without reading it
// an acceptance that reported `blocked`, i.e. "I could not accept this batch",
// would render in this column as a plain "done", which is the opposite of what
// happened. Subs[0] is the right place to look because `pm finish` seeds exactly
// one sub for the tracker it is accepting (the claim count is summed over Subs
// anyway, so a future multi-sub shape would still total correctly).
//
// Only the three CONTRACTED words are promoted. Unlike the RUN column, whose
// status words all come from pm's own constants, this one is written from a
// language model's structured output, and nothing between the envelope and the
// run-state normalises it (see parseFinishResult / scanFinishStructuredOutput).
// A worker answering "Done" or "needs a human" must not end up defining a state
// in the JSON contract the board is built on; the raw word survives in the
// run-state and in the acceptance report, where it belongs.
func acceptVerdict(st *RunState) string {
	if len(st.Subs) == 0 {
		return ""
	}
	switch v := st.Subs[0].Status; v {
	case AcceptCellDone, AcceptCellPartial, AcceptCellBlocked:
		return v
	default:
		return ""
	}
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
//
// The date is parsed IN THE LOCAL ZONE, because that is where it was written
// (storage.Today formats time.Now()). Reading it as UTC midnight would shift a
// whole day's tasks east of UTC by the offset, so a run that finished at 01:30
// local - stamped the previous day in UTC - would sort below a tracker whose
// task file was merely touched today.
func parseRunStamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if at, err := time.Parse(time.RFC3339, s); err == nil {
		return at, true
	}
	if at, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return at, true
	}
	return time.Time{}, false
}
