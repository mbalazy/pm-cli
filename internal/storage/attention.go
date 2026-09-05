package storage

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The attention aggregation: the cross-project queue of what needs the human,
// computed ONCE here and consumed by three surfaces - `pm serve`
// (/api/attention), `pm today` and `pm_context`'s `attention` block - so the
// user's home screen and a Claude session read the same picture (the
// LocalRunRows/BuildTrackers rule: one counting rule, N displays; a client
// never recounts).
//
// Inputs are LOCAL FILES ONLY: tasks of the projects that are not asleep
// (Project.Archived), run/acceptance states and claims under .executor/,
// focus.yaml, project.yaml (group), and the cockpit block of config.yaml
// (thresholds, section toggles). No network - the change feed is a separate
// mechanism (118-14) and this aggregation only EMBEDS a digest of it, handed
// in by the caller, never fetched.
//
// The definition (sections, their order, the ranking inside needs_me, the
// thresholds, the action vocabulary) is the user's decision of 2026-09-05
// (pm-cli-118-8/-13). The thresholds are v1 and live in the config, not here.

// Section names, in the home screen's display order. The list is
// CockpitSections (cockpit.go); the constants exist so a row's Section is
// never a literal at a call site.
const (
	SectionNeedsMe        = "needs_me"
	SectionLandedNoPR     = "landed_no_pr"
	SectionFocus          = "focus"
	SectionInProgress     = "in_progress"
	SectionWaiting        = "waiting"
	SectionChanges        = "changes"
	SectionStuckProjects  = "stuck_projects"
	SectionNewSinceCutoff = "new_since_cutoff"
	SectionRecent         = "recent"
)

// Severity vocabulary, worst first in the order SeverityRank knows.
const (
	SeverityCrit = "crit"
	SeverityWarn = "warn"
	SeverityInfo = "info"
	SeverityOK   = "ok"
)

// SeverityRank orders severities worst-first (crit=0). Unknown = last.
func SeverityRank(s string) int {
	switch s {
	case SeverityCrit:
		return 0
	case SeverityWarn:
		return 1
	case SeverityInfo:
		return 2
	case SeverityOK:
		return 3
	}
	return 4
}

// Actions a row may carry: the CLOSED set. Which of them a row carries is
// decided HERE from the row's state (the Agent Inbox pattern) - a UI renders
// what it is given and invents nothing. Which of them need a confirmation
// dialog is the UI's decision from the user's rule: everything that starts a
// process or changes a run's state (claim, rerun_finish, resume_run, kill).
const (
	ActionOpen          = "open"
	ActionReport        = "report"
	ActionClaim         = "claim"
	ActionRerunFinish   = "rerun_finish"
	ActionResumeRun     = "resume_run"
	ActionKill          = "kill"
	ActionFocusToggle   = "focus_toggle"
	ActionSetWaitingFor = "set_waiting_for"
	ActionBackToTodo    = "back_to_todo"
	ActionMarkSeen      = "mark_seen"
	ActionSleepProject  = "sleep_project"
	ActionOpenPR        = "open_pr"
	// ActionReleaseClaim drops an acceptance claim the COCKPIT itself holds
	// (a claim whose session carries the cockpit prefix, on this host) -
	// the one claim a web button may release without taking it from anyone.
	ActionReleaseClaim = "release_claim"
)

// Flags a row may carry.
const (
	FlagNoReason  = "no_reason"  // a waiting task with no waiting_for - an ALARM, not a blank
	FlagHighlight = "highlight"  // waiting longer than waiting_highlight_days
	FlagStale     = "stale_plan" // a focus row carried over from an older plan
)

// AttentionRow is ONE shape for every section, so a renderer has one row
// component. TaskID is empty on a project-level row (stuck_projects).
type AttentionRow struct {
	Section  string `json:"section"`
	Severity string `json:"severity"`
	Project  string `json:"project"`
	Group    string `json:"group"`
	TaskID   string `json:"task_id,omitempty"`
	Title    string `json:"title"`
	// Status is the task's status, for a chip; empty on project rows.
	Status string `json:"status,omitempty"`
	// Reason is one display-ready sentence: WHY the row is here.
	Reason string `json:"reason"`
	// AgeSeconds is how long the row's condition has held; nil = unknown,
	// rendered "since ?" and NEVER guessed from another stamp.
	AgeSeconds *int64 `json:"age_seconds"`
	// Since is the raw stamp AgeSeconds was measured from, when known.
	Since   string   `json:"since,omitempty"`
	Flags   []string `json:"flags,omitempty"`
	Actions []string `json:"actions"`

	// rank orders rows inside needs_me (lower = worse); unexported, sorting only.
	rank int
}

// AttentionSection is one section of the queue. A section switched off in
// the config is not emitted at all. Total is the row count BEFORE any
// budget a consumer applies (pm_context caps waiting at ten), so a capped
// list still says how many there are.
type AttentionSection struct {
	Name  string         `json:"name"`
	Rows  []AttentionRow `json:"rows"`
	Total int            `json:"total"`
	// Note explains an empty or unsupported section ("no change feed yet").
	Note string `json:"note,omitempty"`
}

// GroupSummary is the sidebar's line for one group: the worst severity among
// its rows, the four counters the sidebar shows as columns, and the group's
// last task activity.
type GroupSummary struct {
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Projects []string `json:"projects"`
	Worst    string   `json:"worst"`
	// Failed counts needs_me rows of the two failed ranks (run failed/crashed,
	// acceptance failed); Visual sums open visual claims; Waiting counts
	// waiting tasks; Quiet counts member repos that meet the stuck rule.
	Failed  int `json:"failed"`
	Visual  int `json:"visual"`
	Waiting int `json:"waiting"`
	Quiet   int `json:"quiet"`
	// LastActivity is the newest task activity stamp in the group.
	LastActivity string `json:"last_activity,omitempty"`
}

// Attention is the whole queue.
type Attention struct {
	// Generated is when this was computed (RFC3339).
	Generated string `json:"generated"`
	// WIP is the number of doing tasks with activity this week (Mon-today) -
	// one number for the header, deliberately not a section.
	WIP      int                `json:"wip"`
	Sections []AttentionSection `json:"sections"`
	Groups   []GroupSummary     `json:"groups"`
}

// ChangesDigest is what the change feed hands the aggregation: a count and
// the first few events already shaped as rows. The aggregation never reads
// the feed's cache itself, so a missing feed is a nil digest.
type ChangesDigest struct {
	Count int
	Rows  []AttentionRow
}

// AttentionOptions tunes one computation.
type AttentionOptions struct {
	// Now is the clock; zero = time.Now(). Injected so the age tests do not
	// wait.
	Now time.Time
	// Changes is the feed digest for the `changes` section; nil = no feed.
	Changes *ChangesDigest
	// ChangesRows caps how many feed rows the section carries (0 = 5).
	ChangesRows int
}

// Section returns the named section or nil when it is off/absent.
func (a *Attention) Section(name string) *AttentionSection {
	if a == nil {
		return nil
	}
	for i := range a.Sections {
		if a.Sections[i].Name == name {
			return &a.Sections[i]
		}
	}
	return nil
}

// Scope narrows the queue to one project or one group (rows and group
// summaries); an empty scope is the identity. Sections keep their order and
// their Total is recomputed for the narrowed rows. Project scope keeps the
// rows of that project only; group scope keeps every member's.
func (a *Attention) Scope(project, group string) *Attention {
	if a == nil || (project == "" && group == "") {
		return a
	}
	keep := func(p, g string) bool {
		if project != "" {
			return p == project
		}
		return g == group
	}
	out := &Attention{Generated: a.Generated, WIP: a.WIP}
	for _, sec := range a.Sections {
		ns := AttentionSection{Name: sec.Name, Note: sec.Note, Rows: []AttentionRow{}}
		for _, r := range sec.Rows {
			if keep(r.Project, r.Group) {
				ns.Rows = append(ns.Rows, r)
			}
		}
		ns.Total = len(ns.Rows)
		out.Sections = append(out.Sections, ns)
	}
	out.Groups = []GroupSummary{}
	for _, g := range a.Groups {
		if group != "" && g.Slug == group {
			out.Groups = append(out.Groups, g)
			continue
		}
		if project != "" {
			for _, p := range g.Projects {
				if p == project {
					out.Groups = append(out.Groups, g)
					break
				}
			}
		}
	}
	return out
}

// projectView is what the aggregation reads once per active project.
type projectView struct {
	slug    string
	group   string
	dir     string
	tasks   []*Task
	byID    map[string]*Task
	landing []TaskStatus
	runs    map[string]*RunState
	accepts map[string]*RunState
	claims  map[string]*FinishClaim
}

// BuildAttention computes the queue over every active project. cfg is the
// cockpit block (nil = defaults). Fails only when the store itself cannot be
// listed or a project's tasks cannot be read - a broken local store, not a
// condition to paper over with an empty queue.
func BuildAttention(store TaskStore, cfg *CockpitConfig, opts AttentionOptions) (*Attention, error) {
	if cfg == nil {
		c := DefaultCockpitConfig()
		cfg = &c
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	slugs, err := store.ListActiveProjects()
	if err != nil {
		return nil, err
	}
	// Group names come off the store's own derivation, so the sidebar and
	// /api/groups agree on what a group is called.
	groups, err := store.ProjectGroups()
	if err != nil {
		return nil, err
	}
	groupName := make(map[string]string, len(groups))
	for _, g := range groups {
		groupName[g.Slug] = g.Name
	}

	var views []*projectView
	for _, slug := range slugs {
		proj, err := store.GetProject(slug)
		if err == nil && proj.Archived {
			continue // ListActiveProjects keeps an unreadable project; a readable asleep one is out
		}
		tasks, err := store.GetTasks(slug)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", slug, err)
		}
		dir := store.ProjectDir(slug)
		v := &projectView{
			slug:    slug,
			group:   proj.GroupSlug(slug),
			dir:     dir,
			tasks:   tasks,
			byID:    make(map[string]*Task, len(tasks)),
			landing: store.GetLandingStatuses(slug),
			runs:    ReadRunStates(dir),
			accepts: ReadFinishRunStates(dir),
			claims:  LiveFinishClaims(dir),
		}
		for _, t := range tasks {
			v.byID[t.Meta.ID] = t
		}
		views = append(views, v)
	}

	focus, focusErr := ReadFocusPlan(store.RootDir())
	if focusErr != nil {
		focus = FocusPlan{}
	}
	onFocus := make(map[string]bool, len(focus.Tasks))
	for _, id := range focus.Tasks {
		onFocus[id] = true
	}

	a := &Attention{Generated: now.Format(time.RFC3339)}
	byName := map[string][]AttentionRow{}
	stuckRepos := map[string]int{} // group -> quiet count
	for _, v := range views {
		byName[SectionNeedsMe] = append(byName[SectionNeedsMe], needsMeRows(v, now)...)
		byName[SectionLandedNoPR] = append(byName[SectionLandedNoPR], landedNoPRRows(v, now)...)
		byName[SectionInProgress] = append(byName[SectionInProgress], inProgressRows(v, now)...)
		byName[SectionWaiting] = append(byName[SectionWaiting], waitingRows(v, cfg, now)...)
		if row, stuck := stuckProjectRow(v, cfg, onFocus, now); stuck {
			byName[SectionStuckProjects] = append(byName[SectionStuckProjects], row)
			stuckRepos[v.group]++
		}
		byName[SectionNewSinceCutoff] = append(byName[SectionNewSinceCutoff], newSinceCutoffRows(v, cfg, now)...)
		a.WIP += wipCount(v, now)
	}
	byName[SectionFocus] = focusRows(views, focus, now)
	sortNeedsMe(byName[SectionNeedsMe])
	sortWaiting(byName[SectionWaiting])
	sortByAgeDesc(byName[SectionLandedNoPR])
	sortByAgeDesc(byName[SectionStuckProjects])
	sortByAgeAsc(byName[SectionNewSinceCutoff])

	for _, name := range CockpitSections {
		if !cfg.SectionEnabled(name) {
			continue
		}
		sec := AttentionSection{Name: name, Rows: byName[name]}
		switch name {
		case SectionChanges:
			sec = changesSection(opts)
		case SectionRecent:
			// The rule ("a session file touched in the last 24h") needs the
			// Claude session-file lookup, which today lives in the board's
			// session code; the section stays configurable and says so.
			sec.Note = "recent is not computed in this version - the session-file lookup is not available to the aggregation yet"
		}
		if sec.Rows == nil {
			sec.Rows = []AttentionRow{}
		}
		if name != SectionChanges {
			sec.Total = len(sec.Rows) // changes carries the feed's own count
		}
		a.Sections = append(a.Sections, sec)
	}

	a.Groups = groupSummaries(views, groups, byName, stuckRepos)
	return a, nil
}

// --- needs_me ---

// Ranks inside needs_me, worst first, as decided: (a) run failed/crashed,
// (b) acceptance failed, (c) open visual claims, (d) landed with no
// acceptance and no claim, (e) acceptance partial, (f) a live claim.
const (
	rankRunFailed = iota
	rankAcceptFailed
	rankVisualClaims
	rankNoAcceptance
	rankPartial
	rankLiveClaim
)

func needsMeRows(v *projectView, now time.Time) []AttentionRow {
	trackers, _ := BuildTrackers(v.tasks, v.landing...)
	var rows []AttentionRow
	for _, tr := range trackers {
		// A tracker the human closed (done / archived) is out whatever its
		// run-state files say: the files are the machine's memory of the last
		// run, the status is the human's decision, and the decision wins.
		if closedByHuman(TaskStatus(tr.Status)) {
			continue
		}
		run := v.runs[tr.ID]
		accept := v.accepts[tr.ID]
		claim := v.claims[tr.ID]
		base := AttentionRow{
			Section: SectionNeedsMe, Project: v.slug, Group: v.group,
			TaskID: tr.ID, Title: tr.Title, Status: tr.Status,
		}
		hasReport := accept != nil && accept.Status != RunStatusRunning

		// (a) the run itself died.
		if run != nil {
			switch {
			case run.Status == RunStatusFailed:
				r := base
				r.rank, r.Severity = rankRunFailed, SeverityCrit
				r.Reason = "run failed" + errSuffix(run.Error)
				r.Since, r.AgeSeconds = ageFrom(run.Updated, now)
				r.Actions = []string{ActionOpen, ActionResumeRun}
				rows = append(rows, r)
				continue
			case run.Status == RunStatusRunning && !run.IsLive():
				r := base
				r.rank, r.Severity = rankRunFailed, SeverityCrit
				r.Reason = "run crashed - marked running but its process is gone"
				r.Since, r.AgeSeconds = ageFrom(run.Updated, now)
				r.Actions = []string{ActionOpen, ActionResumeRun}
				rows = append(rows, r)
				continue
			}
		}

		// (f) a live claim: somebody is on it. Checked before the
		// acceptance state, the acceptCell rule - the claim is the artifact
		// designed to be read across machines.
		if claim != nil {
			r := base
			r.rank, r.Severity = rankLiveClaim, SeverityOK
			r.Reason = "acceptance in progress on " + claim.Host
			r.Since, r.AgeSeconds = ageFrom(claim.Started, now)
			r.Actions = []string{ActionOpen}
			if accept != nil && accept.IsLive() {
				r.Actions = append(r.Actions, ActionKill)
			}
			if CockpitOwnsClaim(claim) {
				r.Reason = "claimed from the cockpit (this machine) - release it when done"
				r.Actions = append(r.Actions, ActionReleaseClaim)
			}
			rows = append(rows, r)
			continue
		}

		if accept != nil {
			switch {
			case accept.Status == RunStatusFailed || (accept.Status == RunStatusRunning && !accept.IsLive()):
				r := base
				r.rank, r.Severity = rankAcceptFailed, SeverityCrit
				r.Reason = "acceptance failed" + errSuffix(accept.Error)
				if accept.Status == RunStatusRunning {
					r.Reason = "acceptance crashed - marked running but its process is gone"
				}
				r.Since, r.AgeSeconds = ageFrom(accept.Updated, now)
				r.Actions = []string{ActionOpen, ActionRerunFinish, ActionClaim}
				if hasReport {
					r.Actions = append(r.Actions, ActionReport)
				}
				rows = append(rows, r)
				continue
			case accept.Status == RunStatusRunning:
				// Live acceptance without a claim file: in_progress covers it.
				continue
			}
			if n := accept.VisualClaimsOpen(); n > 0 {
				r := base
				r.rank, r.Severity = rankVisualClaims, SeverityWarn
				r.Reason = fmt.Sprintf("%d visual claim(s) waiting for a look at the screen", n)
				r.Since, r.AgeSeconds = ageFrom(accept.Updated, now)
				r.Actions = []string{ActionOpen, ActionReport, ActionClaim, ActionRerunFinish}
				rows = append(rows, r)
				continue
			}
			if acceptVerdict(accept) == AcceptCellPartial {
				r := base
				r.rank, r.Severity = rankPartial, SeverityWarn
				r.Reason = "acceptance partial - part of the batch is still open"
				r.Since, r.AgeSeconds = ageFrom(accept.Updated, now)
				r.Actions = []string{ActionOpen, ActionReport, ActionClaim, ActionRerunFinish}
				rows = append(rows, r)
				continue
			}
			continue // accepted (done/blocked): nothing for this section
		}

		// (d) the run landed and nobody accepted it.
		if run != nil && run.Status == RunStatusDone {
			r := base
			r.rank, r.Severity = rankNoAcceptance, SeverityWarn
			r.Reason = "run finished, no acceptance yet"
			r.Since, r.AgeSeconds = ageFrom(run.Updated, now)
			r.Actions = []string{ActionOpen, ActionClaim, ActionRerunFinish}
			rows = append(rows, r)
		}
	}
	return rows
}

// closedByHuman: the two statuses only a person sets on a tracker to say
// "this is over" - done (the project's own terminal status) and archived
// (system-level). Landing statuses (merged / pushed) are the executor's and
// do not count: a tracker on them is still waiting for its acceptance.
func closedByHuman(s TaskStatus) bool {
	return s == StatusDone || s == StatusArchived
}

func errSuffix(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	if len(msg) > 120 {
		msg = msg[:117] + "..."
	}
	return ": " + msg
}

func sortNeedsMe(rows []AttentionRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].rank != rows[j].rank {
			return rows[i].rank < rows[j].rank
		}
		return olderFirst(rows[i], rows[j])
	})
}

// --- landed_no_pr ---

// landedNoPRRows: a task on a landing status whose parent was accepted
// (done/partial) and that carries no `pr` link - work that is finished and
// checked but never handed on.
func landedNoPRRows(v *projectView, now time.Time) []AttentionRow {
	accepted := map[string]bool{}
	for id, st := range v.accepts {
		if vd := acceptVerdict(st); vd == AcceptCellDone || vd == AcceptCellPartial {
			accepted[id] = true
		}
	}
	var rows []AttentionRow
	for _, t := range v.tasks {
		if t.Meta.Parent == "" || !accepted[t.Meta.Parent] {
			continue
		}
		if parent, ok := v.byID[t.Meta.Parent]; ok && closedByHuman(parent.Meta.Status) {
			continue // the human closed the tracker: its leftovers are not a to-do
		}
		if !onStatus(t.Meta.Status, v.landing) || t.Meta.Links["pr"] != "" {
			continue
		}
		r := AttentionRow{
			Section: SectionLandedNoPR, Severity: SeverityInfo, Project: v.slug, Group: v.group,
			TaskID: t.Meta.ID, Title: t.Meta.Title, Status: string(t.Meta.Status),
			Reason:  "accepted, no PR opened",
			Actions: []string{ActionOpen, ActionFocusToggle},
		}
		r.Since, r.AgeSeconds = ageFrom(t.Meta.StatusChanged, now)
		rows = append(rows, r)
	}
	return rows
}

func onStatus(s TaskStatus, list []TaskStatus) bool {
	for _, l := range list {
		if l == s {
			return true
		}
	}
	return false
}

// --- focus ---

// focusRows lists the plan in its own order. An older plan is carried over
// the way the board does it (FocusPlan.Cleanup drops closed and vanished
// tasks); its rows carry FlagStale so the header can say "from <date>".
func focusRows(views []*projectView, focus FocusPlan, now time.Time) []AttentionRow {
	if len(focus.Tasks) == 0 {
		return nil
	}
	var all []*Task
	byID := map[string]*projectView{}
	for _, v := range views {
		all = append(all, v.tasks...)
		for id := range v.byID {
			byID[id] = v
		}
	}
	plan := focus
	plan.Cleanup(all)
	// Against the injected clock, not FocusPlan.IsStale's wall clock.
	stale := plan.Date != "" && plan.Date != now.Format("2006-01-02")
	var rows []AttentionRow
	for _, id := range plan.Tasks {
		v := byID[id]
		if v == nil {
			continue
		}
		t := v.byID[id]
		r := AttentionRow{
			Section: SectionFocus, Severity: SeverityInfo, Project: v.slug, Group: v.group,
			TaskID: t.Meta.ID, Title: t.Meta.Title, Status: string(t.Meta.Status),
			Reason:  "on today's focus",
			Actions: taskActions(t),
		}
		if stale {
			r.Reason = "on the focus plan from " + focus.Date
			r.Flags = append(r.Flags, FlagStale)
		}
		r.Since, r.AgeSeconds = ageFrom(t.Meta.StatusChanged, now)
		rows = append(rows, r)
	}
	return rows
}

// taskActions is the action set of an ordinary task row.
func taskActions(t *Task) []string {
	acts := []string{ActionOpen, ActionFocusToggle}
	switch t.Meta.Status {
	case StatusDoing:
		acts = append(acts, ActionSetWaitingFor, ActionBackToTodo)
	case StatusWaiting:
		acts = append(acts, ActionSetWaitingFor, ActionBackToTodo)
	}
	if t.Meta.Links["pr"] != "" {
		acts = append(acts, ActionOpenPR)
	}
	return acts
}

// CockpitClaimPrefix is the session prefix of a claim the web cockpit
// takes (`cockpit-<host>`); runctl writes it, this reads it.
const CockpitClaimPrefix = "cockpit-"

// CockpitOwnsClaim reports whether a live claim was taken by the cockpit on
// THIS host - the one claim a web button may release without taking a run
// from another session.
func CockpitOwnsClaim(c *FinishClaim) bool {
	return c != nil && c.Host == hostname() && strings.HasPrefix(c.Session, CockpitClaimPrefix)
}

// --- in_progress ---

func inProgressRows(v *projectView, now time.Time) []AttentionRow {
	var rows []AttentionRow
	emit := func(st *RunState, kind string) {
		if !st.IsLive() {
			return
		}
		title := st.TaskID
		if t := v.byID[st.TaskID]; t != nil {
			title = t.Meta.Title
		}
		r := AttentionRow{
			Section: SectionInProgress, Severity: SeverityOK, Project: v.slug, Group: v.group,
			TaskID: st.TaskID, Title: title,
			Actions: []string{ActionOpen, ActionKill},
		}
		if t := v.byID[st.TaskID]; t != nil {
			r.Status = string(t.Meta.Status)
		}
		parts := []string{kind + " running"}
		if st.Phase != "" {
			parts = append(parts, "phase "+st.Phase)
		}
		if st.CurrentSub != "" {
			parts = append(parts, "sub "+st.CurrentSub)
		}
		if beat, ok := ParseStamp(st.Updated); ok {
			parts = append(parts, "heartbeat "+HumanAge(now.Sub(beat))+" ago")
		}
		r.Reason = strings.Join(parts, " · ")
		r.Since, r.AgeSeconds = ageFrom(st.Started, now)
		rows = append(rows, r)
	}
	for _, st := range v.runs {
		emit(st, st.Kind)
	}
	for _, st := range v.accepts {
		emit(st, "acceptance")
	}
	sortByAgeDesc(rows)
	return rows
}

// --- waiting ---

func waitingRows(v *projectView, cfg *CockpitConfig, now time.Time) []AttentionRow {
	var rows []AttentionRow
	for _, t := range v.tasks {
		if t.Meta.Status != StatusWaiting {
			continue
		}
		r := AttentionRow{
			Section: SectionWaiting, Severity: SeverityInfo, Project: v.slug, Group: v.group,
			TaskID: t.Meta.ID, Title: t.Meta.Title, Status: string(t.Meta.Status),
			Actions: taskActions(t),
		}
		if strings.TrimSpace(t.Meta.WaitingFor) == "" {
			r.Reason = "waiting - no reason recorded"
			r.Flags = append(r.Flags, FlagNoReason)
			r.Severity = SeverityWarn
		} else {
			r.Reason = "waiting for " + t.Meta.WaitingFor
		}
		// Age from status_changed ONLY: `updated` moves on every edit and
		// would reset the clock this row exists to show. Empty = unknown.
		r.Since, r.AgeSeconds = ageFrom(t.Meta.StatusChanged, now)
		if r.AgeSeconds != nil && *r.AgeSeconds > int64(cfg.WaitingHighlightDays)*86400 {
			r.Flags = append(r.Flags, FlagHighlight)
			r.Severity = SeverityWarn
		}
		rows = append(rows, r)
	}
	return rows
}

// sortWaiting: oldest first, unknown age last, no_reason first among equals
// is NOT applied here - the raw section is age-ordered; pm_context's budget
// promotes no_reason rows itself.
func sortWaiting(rows []AttentionRow) {
	sort.SliceStable(rows, func(i, j int) bool { return olderFirst(rows[i], rows[j]) })
}

// --- stuck_projects ---

// stuckProjectRow applies the two rules: no task change at all for
// stuck_project_days, OR every doing task idle for doing_idle_days with
// nothing of the project on focus. Activity = max(updated, status_changed) -
// deliberately a different measure from "waiting since", where updated is
// forbidden. Rule two needs at least one doing task: with none it would be
// vacuously true of every backlog-only project.
func stuckProjectRow(v *projectView, cfg *CockpitConfig, onFocus map[string]bool, now time.Time) (AttentionRow, bool) {
	var last time.Time
	doing, idleDoing := 0, 0
	focused := false
	for _, t := range v.tasks {
		if at, ok := activity(t); ok && at.After(last) {
			last = at
		}
		if onFocus[t.Meta.ID] {
			focused = true
		}
		if t.Meta.Status == StatusDoing {
			doing++
			at, ok := activity(t)
			if !ok || now.Sub(at) > time.Duration(cfg.DoingIdleDays)*24*time.Hour {
				idleDoing++
			}
		}
	}
	row := AttentionRow{
		Section: SectionStuckProjects, Severity: SeverityWarn, Project: v.slug, Group: v.group,
		Title:   v.slug,
		Actions: []string{ActionOpen, ActionSleepProject},
	}
	if last.IsZero() {
		if len(v.tasks) == 0 {
			return row, false // nothing to be stuck on
		}
		row.Reason = "no task activity on record"
		return row, true
	}
	silent := now.Sub(last) > time.Duration(cfg.StuckProjectDays)*24*time.Hour
	if silent {
		row.Reason = fmt.Sprintf("no task change for %s", HumanAge(now.Sub(last)))
		row.Since, row.AgeSeconds = stampAge(last, now)
		return row, true
	}
	if doing > 0 && idleDoing == doing && !focused {
		row.Reason = fmt.Sprintf("every doing task idle for %dd+ and nothing on focus", cfg.DoingIdleDays)
		row.Since, row.AgeSeconds = stampAge(last, now)
		return row, true
	}
	return row, false
}

// activity is a task's last activity: the later of updated and status_changed.
func activity(t *Task) (time.Time, bool) {
	var best time.Time
	ok := false
	for _, s := range []string{t.Meta.Updated, t.Meta.StatusChanged} {
		if at, valid := ParseStamp(s); valid && at.After(best) {
			best, ok = at, true
		}
	}
	return best, ok
}

// --- new_since_cutoff ---

// newSinceCutoffRows: tasks created on or after the cutoff's DATE. `created`
// is a bare date, so an 18:00 cutoff is approximated to "since yesterday".
func newSinceCutoffRows(v *projectView, cfg *CockpitConfig, now time.Time) []AttentionRow {
	cutoff := CutoffSince(cfg.CutoffHour, now)
	cutoffDay := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(), 0, 0, 0, 0, cutoff.Location())
	var rows []AttentionRow
	for _, t := range v.tasks {
		created, ok := ParseStamp(t.Meta.Created)
		if !ok || created.Before(cutoffDay) || t.Meta.Status == StatusArchived {
			continue
		}
		r := AttentionRow{
			Section: SectionNewSinceCutoff, Severity: SeverityInfo, Project: v.slug, Group: v.group,
			TaskID: t.Meta.ID, Title: t.Meta.Title, Status: string(t.Meta.Status),
			Reason:  "created " + t.Meta.Created,
			Actions: taskActions(t),
		}
		r.Since, r.AgeSeconds = ageFrom(t.Meta.Created, now)
		rows = append(rows, r)
	}
	return rows
}

// CutoffSince is the start of the "since yesterday" window: before the
// cutoff hour today it is yesterday at that hour, from the cutoff hour on it
// is today at that hour. Local time - the hour is a wall-clock fact.
func CutoffSince(hour int, now time.Time) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if now.Before(today) {
		return today.AddDate(0, 0, -1)
	}
	return today
}

// --- changes ---

func changesSection(opts AttentionOptions) AttentionSection {
	sec := AttentionSection{Name: SectionChanges, Rows: []AttentionRow{}}
	if opts.Changes == nil {
		sec.Note = "no change feed yet"
		return sec
	}
	n := opts.ChangesRows
	if n <= 0 {
		n = 5
	}
	rows := opts.Changes.Rows
	if len(rows) > n {
		rows = rows[:n]
	}
	sec.Rows = append(sec.Rows, rows...)
	sec.Total = opts.Changes.Count
	return sec
}

// --- wip ---

// wipCount: doing tasks with activity this week (Monday 00:00 local to now).
func wipCount(v *projectView, now time.Time) int {
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	monday := time.Date(now.Year(), now.Month(), now.Day()-(weekday-1), 0, 0, 0, 0, now.Location())
	n := 0
	for _, t := range v.tasks {
		if t.Meta.Status != StatusDoing {
			continue
		}
		if at, ok := activity(t); ok && !at.Before(monday) {
			n++
		}
	}
	return n
}

// --- groups ---

func groupSummaries(views []*projectView, groups []ProjectGroup, byName map[string][]AttentionRow, quiet map[string]int) []GroupSummary {
	out := make([]GroupSummary, 0, len(groups))
	idx := map[string]int{}
	for _, g := range groups {
		idx[g.Slug] = len(out)
		out = append(out, GroupSummary{Slug: g.Slug, Name: g.Name, Projects: g.Projects, Worst: SeverityOK, Quiet: quiet[g.Slug]})
	}
	bump := func(g string, sev string) {
		i, ok := idx[g]
		if !ok {
			return
		}
		if SeverityRank(sev) < SeverityRank(out[i].Worst) {
			out[i].Worst = sev
		}
	}
	for name, rows := range byName {
		for _, r := range rows {
			bump(r.Group, r.Severity)
			i, ok := idx[r.Group]
			if !ok {
				continue
			}
			switch name {
			case SectionNeedsMe:
				if r.rank == rankRunFailed || r.rank == rankAcceptFailed {
					out[i].Failed++
				}
			case SectionWaiting:
				out[i].Waiting++
			}
		}
	}
	for _, v := range views {
		i, ok := idx[v.group]
		if !ok {
			continue
		}
		for _, st := range v.accepts {
			// The same rule as needs_me (closedByHuman): a tracker on done or
			// archived is closed whatever its finish.json still says, so its
			// claims are not a 👁 count with no row behind it.
			if tr, ok := v.byID[st.TaskID]; ok && closedByHuman(tr.Meta.Status) {
				continue
			}
			if v.claims[st.TaskID] == nil {
				out[i].Visual += st.VisualClaimsOpen()
			}
		}
		var last time.Time
		for _, t := range v.tasks {
			if at, ok := activity(t); ok && at.After(last) {
				last = at
			}
		}
		if !last.IsZero() {
			if cur, ok := ParseStamp(out[i].LastActivity); !ok || last.After(cur) {
				out[i].LastActivity = last.Format(time.RFC3339)
			}
		}
	}
	// Default sort: worst first, then most recent activity, then slug. The
	// config's alternatives (last_activity, manual) are the consumer's to
	// apply - the data carries what both need.
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := SeverityRank(out[i].Worst), SeverityRank(out[j].Worst); a != b {
			return a < b
		}
		ai, aok := ParseStamp(out[i].LastActivity)
		bi, bok := ParseStamp(out[j].LastActivity)
		if aok != bok {
			return aok
		}
		if aok && !ai.Equal(bi) {
			return ai.After(bi)
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}

// --- ages ---

// ageFrom measures now - stamp; an empty or unparsable stamp yields nil
// (unknown), never a guess from another field.
func ageFrom(stamp string, now time.Time) (string, *int64) {
	at, ok := ParseStamp(stamp)
	if !ok {
		return "", nil
	}
	return stampAge(at, now)
}

func stampAge(at, now time.Time) (string, *int64) {
	secs := int64(now.Sub(at) / time.Second)
	if secs < 0 {
		secs = 0
	}
	return at.Format(time.RFC3339), &secs
}

// olderFirst orders by age descending; unknown ages last; then by task id.
func olderFirst(a, b AttentionRow) bool {
	switch {
	case a.AgeSeconds == nil && b.AgeSeconds == nil:
		return a.Project+a.TaskID < b.Project+b.TaskID
	case a.AgeSeconds == nil:
		return false
	case b.AgeSeconds == nil:
		return true
	case *a.AgeSeconds != *b.AgeSeconds:
		return *a.AgeSeconds > *b.AgeSeconds
	}
	return a.Project+a.TaskID < b.Project+b.TaskID
}

func sortByAgeDesc(rows []AttentionRow) {
	sort.SliceStable(rows, func(i, j int) bool { return olderFirst(rows[i], rows[j]) })
}

func sortByAgeAsc(rows []AttentionRow) {
	sort.SliceStable(rows, func(i, j int) bool { return olderFirst(rows[j], rows[i]) })
}

// HumanAge renders a duration the way the queue speaks about it: "3d",
// "5h", "12m", "40s". One spelling for every surface.
func HumanAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// AgeString renders a row's age for text surfaces: "since ?" when unknown.
func (r AttentionRow) AgeString() string {
	if r.AgeSeconds == nil {
		return "since ?"
	}
	return HumanAge(time.Duration(*r.AgeSeconds) * time.Second)
}
