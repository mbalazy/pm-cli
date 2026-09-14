package storage

import (
	"os"
	"strings"
	"testing"
	"time"
)

// attentionNow is the fixed clock every attention test measures ages
// against: a Wednesday afternoon, so "this week" spans Mon-Wed.
var attentionNow = time.Date(2026, 9, 9, 15, 0, 0, 0, time.Local)

func stampAgo(d time.Duration) string { return attentionNow.Add(-d).Format(time.RFC3339) }
func dateAgo(days int) string         { return attentionNow.AddDate(0, 0, -days).Format("2006-01-02") }

// attentionStore is the fixture the AC lists: a run failed, an acceptance
// with visual claims, a run landed without acceptance, a partial
// acceptance, a live claim, waiting tasks with and without a reason and
// without status_changed, a project stuck by each rule, an asleep project,
// and a group of two repos.
// attentionTask writes the task file directly (AddTask re-stamps `updated`,
// and the fixture's stamps ARE the test).
func attentionTask(t *testing.T, store *Store, slug string, meta TaskMeta) {
	t.Helper()
	task := &Task{Meta: meta, Project: slug, FilePath: store.ProjectDir(slug) + "/" + meta.ID + ".md"}
	if err := store.WriteTask(task); err != nil {
		t.Fatal(err)
	}
}

func attentionStore(t *testing.T) *Store {
	t.Helper()
	store := &Store{Root: t.TempDir()}
	mk := func(slug, group string, archived bool) {
		if err := store.CreateProject(slug, &Project{
			Name: slug, Prefix: slug, Group: group, Archived: archived,
			Statuses: []string{"todo", "doing", "waiting", "merged", "pushed", "done"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("acme-api", "acme", false)
	mk("acme-zap", "acme", false)
	mk("solo", "", false)
	mk("silent", "", false)
	mk("idle", "", false)
	mk("asleep", "acme", true)
	writeConfig(t, store, "cockpit:\n  groups:\n    acme: {name: ACME}\n")

	tracker := func(slug, id, title string) {
		attentionTask(t, store, slug, TaskMeta{ID: id, Title: title, Status: StatusDoing, Created: dateAgo(3), Updated: stampAgo(3 * time.Hour)})
		attentionTask(t, store, slug, TaskMeta{ID: id + "-1", Title: "child", Status: StatusMerged, Created: dateAgo(3), Updated: stampAgo(3 * time.Hour), Parent: id})
	}
	// acme-api: a failed run, a visual-claims acceptance, a landed-no-acceptance run.
	tracker("acme-api", "acme-api-1", "failed run")
	tracker("acme-api", "acme-api-2", "visual claims")
	tracker("acme-api", "acme-api-3", "landed no acceptance")
	// acme-zap: a partial acceptance (with a child lacking a PR) and a live claim.
	tracker("acme-zap", "acme-zap-1", "partial")
	tracker("acme-zap", "acme-zap-2", "claimed")
	// asleep: a failed run that must appear nowhere.
	tracker("asleep", "asleep-1", "asleep failed")

	dir := store.ProjectDir
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(WriteRunState(dir("acme-api"), &RunState{TaskID: "acme-api-1", Project: "acme-api", Kind: RunKindEpic, Status: RunStatusFailed, PID: 1, Started: stampAgo(10 * time.Hour), Updated: stampAgo(9 * time.Hour), Error: "worker died", Subs: []SubRun{{ID: "acme-api-1-1", Status: "failed"}}}))
	must(WriteRunState(dir("acme-api"), &RunState{TaskID: "acme-api-2", Project: "acme-api", Kind: RunKindEpic, Status: RunStatusDone, PID: 1, Started: stampAgo(30 * time.Hour), Updated: stampAgo(29 * time.Hour), Subs: []SubRun{{ID: "acme-api-2-1", Status: "merged"}}}))
	must(WriteRunState(dir("acme-api"), &RunState{TaskID: "acme-api-2", Project: "acme-api", Kind: RunKindFinish, Status: RunStatusDone, PID: 1, Started: stampAgo(20 * time.Hour), Updated: stampAgo(19 * time.Hour), Subs: []SubRun{{ID: "acme-api-2", Status: "done", VisualClaimsOpen: 2}}}))
	must(WriteRunState(dir("acme-api"), &RunState{TaskID: "acme-api-3", Project: "acme-api", Kind: RunKindEpic, Status: RunStatusDone, PID: 1, Started: stampAgo(50 * time.Hour), Updated: stampAgo(49 * time.Hour), Subs: []SubRun{{ID: "acme-api-3-1", Status: "merged"}}}))
	must(WriteRunState(dir("acme-zap"), &RunState{TaskID: "acme-zap-1", Project: "acme-zap", Kind: RunKindEpic, Status: RunStatusDone, PID: 1, Started: stampAgo(60 * time.Hour), Updated: stampAgo(59 * time.Hour), Subs: []SubRun{{ID: "acme-zap-1-1", Status: "merged"}}}))
	must(WriteRunState(dir("acme-zap"), &RunState{TaskID: "acme-zap-1", Project: "acme-zap", Kind: RunKindFinish, Status: RunStatusDone, PID: 1, Started: stampAgo(40 * time.Hour), Updated: stampAgo(39 * time.Hour), Subs: []SubRun{{ID: "acme-zap-1", Status: "partial"}}}))
	must(WriteRunState(dir("acme-zap"), &RunState{TaskID: "acme-zap-2", Project: "acme-zap", Kind: RunKindEpic, Status: RunStatusDone, PID: 1, Started: stampAgo(8 * time.Hour), Updated: stampAgo(7 * time.Hour), Subs: []SubRun{{ID: "acme-zap-2-1", Status: "merged"}}}))
	if _, err := AcquireFinishClaim(dir("acme-zap"), "acme-zap-2", "sess-1"); err != nil {
		t.Fatal(err)
	}
	must(WriteRunState(dir("asleep"), &RunState{TaskID: "asleep-1", Project: "asleep", Kind: RunKindEpic, Status: RunStatusFailed, PID: 1, Started: stampAgo(time.Hour), Updated: stampAgo(time.Hour)}))

	// waiting: with a reason (old, highlighted), without a reason, without a stamp.
	attentionTask(t, store, "solo", TaskMeta{ID: "solo-1", Title: "waits on client", Status: StatusWaiting, WaitingFor: "client answer", Created: dateAgo(10), Updated: stampAgo(time.Hour), StatusChanged: stampAgo(8 * 24 * time.Hour)})
	attentionTask(t, store, "solo", TaskMeta{ID: "solo-2", Title: "waits silently", Status: StatusWaiting, Created: dateAgo(2), Updated: stampAgo(time.Hour), StatusChanged: stampAgo(2 * 24 * time.Hour)})
	attentionTask(t, store, "solo", TaskMeta{ID: "solo-3", Title: "waits since ?", Status: StatusWaiting, WaitingFor: "review", Created: dateAgo(30), Updated: stampAgo(time.Hour)})
	// a doing task touched this week -> wip, a doing task last week -> not
	attentionTask(t, store, "solo", TaskMeta{ID: "solo-4", Title: "this week", Status: StatusDoing, Created: dateAgo(1), Updated: stampAgo(24 * time.Hour)})
	attentionTask(t, store, "solo", TaskMeta{ID: "solo-5", Title: "last week", Status: StatusDoing, Created: dateAgo(20), Updated: stampAgo(10 * 24 * time.Hour)})
	// silent: rule one (no change for 14d+).
	attentionTask(t, store, "silent", TaskMeta{ID: "silent-1", Title: "old", Status: StatusTodo, Created: dateAgo(40), Updated: stampAgo(20 * 24 * time.Hour)})
	// idle: rule two (every doing task idle 7d+, a fresh todo keeps rule one off).
	attentionTask(t, store, "idle", TaskMeta{ID: "idle-1", Title: "idle doing", Status: StatusDoing, Created: dateAgo(30), Updated: stampAgo(9 * 24 * time.Hour)})
	attentionTask(t, store, "idle", TaskMeta{ID: "idle-2", Title: "fresh todo", Status: StatusTodo, Created: dateAgo(1), Updated: stampAgo(time.Hour)})
	return store
}

func build(t *testing.T, store *Store, cfgBody string, opts AttentionOptions) *Attention {
	t.Helper()
	if cfgBody != "" {
		writeConfig(t, store, cfgBody)
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if opts.Now.IsZero() {
		opts.Now = attentionNow
	}
	// The fixture is about the executor's rows; a body that names the switch
	// tests the switch itself.
	if !strings.Contains(cfgBody, "show_executor") {
		cfg.Cockpit.ShowExecutor = true
	}
	a, err := BuildAttention(store, &cfg.Cockpit, opts)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func ids(rows []AttentionRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.TaskID
		if out[i] == "" {
			out[i] = r.Project
		}
	}
	return out
}

func TestAttentionNeedsMeRanking(t *testing.T) {
	a := build(t, attentionStore(t), "", AttentionOptions{})
	sec := a.Section(SectionNeedsMe)
	if sec == nil {
		t.Fatal("needs_me missing")
	}
	want := []string{"acme-api-1", "acme-api-2", "acme-api-3", "acme-zap-1", "acme-zap-2"}
	if got := ids(sec.Rows); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
	byID := map[string]AttentionRow{}
	for _, r := range sec.Rows {
		byID[r.TaskID] = r
	}
	if r := byID["acme-api-1"]; r.Severity != SeverityCrit || !strings.Contains(r.Reason, "worker died") || !has(r.Actions, ActionResumeRun) || r.Group != "acme" {
		t.Errorf("failed run row = %+v", r)
	}
	if r := byID["acme-api-2"]; r.Severity != SeverityWarn || !strings.Contains(r.Reason, "2 visual claim") || !has(r.Actions, ActionReport) || !has(r.Actions, ActionClaim) {
		t.Errorf("visual row = %+v", r)
	}
	if r := byID["acme-api-3"]; !strings.Contains(r.Reason, "no acceptance") || !has(r.Actions, ActionClaim) || has(r.Actions, ActionReport) {
		t.Errorf("landed row = %+v", r)
	}
	if r := byID["acme-zap-1"]; !strings.Contains(r.Reason, "partial") || !has(r.Actions, ActionRerunFinish) {
		t.Errorf("partial row = %+v", r)
	}
	if r := byID["acme-zap-2"]; r.Severity != SeverityOK || !strings.Contains(r.Reason, "acceptance in progress on") || has(r.Actions, ActionClaim) {
		t.Errorf("claim row = %+v", r)
	}
	for _, r := range sec.Rows {
		if r.Project == "asleep" {
			t.Fatalf("asleep project leaked into needs_me: %+v", r)
		}
		if r.AgeSeconds == nil {
			t.Errorf("%s: age must be known (from the run stamp)", r.TaskID)
		}
	}
}

func TestAttentionCrashedRunIsFailed(t *testing.T) {
	store := attentionStore(t)
	// A run marked running whose pid is gone reads as crashed, rank (a).
	if err := WriteRunState(store.ProjectDir("acme-api"), &RunState{TaskID: "acme-api-3", Project: "acme-api", Kind: RunKindEpic, Status: RunStatusRunning, PID: 999999, Started: stampAgo(3 * time.Hour), Updated: stampAgo(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	a := build(t, store, "", AttentionOptions{})
	rows := a.Section(SectionNeedsMe).Rows
	if len(rows) < 2 || rows[1].TaskID != "acme-api-3" || !strings.Contains(rows[1].Reason, "crashed") || rows[1].Severity != SeverityCrit {
		t.Fatalf("rows = %v", ids(rows))
	}
	if ip := a.Section(SectionInProgress); len(ip.Rows) != 0 {
		t.Errorf("a dead run is not in progress: %+v", ip.Rows)
	}
}

func TestAttentionInProgress(t *testing.T) {
	store := attentionStore(t)
	// Started = now: IsLive compares the pid's start time against the stamp,
	// and this test binary started seconds ago.
	if err := WriteRunState(store.ProjectDir("solo"), &RunState{TaskID: "solo-4", Project: "solo", Kind: RunKindWork, Status: RunStatusRunning, PID: os.Getpid(), Started: time.Now().Format(time.RFC3339), Updated: time.Now().Format(time.RFC3339), Phase: "implement", CurrentSub: "solo-4"}); err != nil {
		t.Fatal(err)
	}
	a := build(t, store, "", AttentionOptions{Now: time.Now()})
	rows := a.Section(SectionInProgress).Rows
	if len(rows) != 1 || rows[0].TaskID != "solo-4" || rows[0].Title != "this week" {
		t.Fatalf("rows = %+v", rows)
	}
	r := rows[0]
	if !strings.Contains(r.Reason, "phase implement") || !strings.Contains(r.Reason, "heartbeat") || !has(r.Actions, ActionKill) || r.Severity != SeverityOK {
		t.Errorf("row = %+v", r)
	}
}

func TestAttentionWaiting(t *testing.T) {
	a := build(t, attentionStore(t), "", AttentionOptions{})
	rows := a.Section(SectionWaiting).Rows
	// Oldest first, unknown age LAST.
	if got := ids(rows); strings.Join(got, ",") != "solo-1,solo-2,solo-3" {
		t.Fatalf("order = %v", got)
	}
	if r := rows[0]; r.WaitingFor != "client answer" {
		t.Errorf("waiting row carries the raw reason for the edit dialog: %+v", r)
	}
	if r := rows[0]; !has(r.Flags, FlagHighlight) || r.Severity != SeverityWarn || r.Reason != "waiting for client answer" {
		t.Errorf("highlighted row = %+v", r)
	}
	if r := rows[1]; !has(r.Flags, FlagNoReason) || r.Severity != SeverityWarn || has(r.Flags, FlagHighlight) {
		t.Errorf("no-reason row = %+v", r)
	}
	if r := rows[2]; r.AgeSeconds != nil || r.Since != "" || r.AgeString() != "since ?" {
		t.Errorf("stampless row must have an unknown age, never one from updated: %+v", r)
	}
	for _, r := range rows {
		if !has(r.Actions, ActionSetWaitingFor) || !has(r.Actions, ActionBackToTodo) {
			t.Errorf("%s actions = %v", r.TaskID, r.Actions)
		}
	}
}

func TestAttentionThresholdsComeFromConfig(t *testing.T) {
	// waiting_highlight_days raised past solo-1's 8 days: no highlight.
	a := build(t, attentionStore(t), "cockpit:\n  waiting_highlight_days: 30\n  doing_idle_days: 30\n  stuck_project_days: 60\n", AttentionOptions{})
	for _, r := range a.Section(SectionWaiting).Rows {
		if has(r.Flags, FlagHighlight) {
			t.Errorf("%s highlighted under a 30d threshold", r.TaskID)
		}
	}
	if rows := a.Section(SectionStuckProjects).Rows; len(rows) != 0 {
		t.Errorf("stuck under raised thresholds: %v", ids(rows))
	}
}

func TestAttentionStuckProjects(t *testing.T) {
	a := build(t, attentionStore(t), "", AttentionOptions{})
	rows := a.Section(SectionStuckProjects).Rows
	if got := ids(rows); strings.Join(got, ",") != "silent,idle" {
		t.Fatalf("stuck = %v", got)
	}
	if !strings.Contains(rows[0].Reason, "no task change for 20d") || !has(rows[0].Actions, ActionSleepProject) {
		t.Errorf("silent row = %+v", rows[0])
	}
	if !strings.Contains(rows[1].Reason, "every doing task idle") {
		t.Errorf("idle row = %+v", rows[1])
	}

	// Putting idle's doing task on focus lifts rule two.
	store := attentionStore(t)
	if err := WriteFocusPlan(store.Root, FocusPlan{Date: attentionNow.Format("2006-01-02"), Tasks: []string{"idle-1"}}); err != nil {
		t.Fatal(err)
	}
	a = build(t, store, "", AttentionOptions{})
	if got := ids(a.Section(SectionStuckProjects).Rows); strings.Join(got, ",") != "silent" {
		t.Errorf("stuck with idle-1 on focus = %v", got)
	}
	if f := a.Section(SectionFocus).Rows; len(f) != 1 || f[0].TaskID != "idle-1" || has(f[0].Flags, FlagStale) {
		t.Errorf("focus = %+v", f)
	}
}

func TestAttentionFocusStalePlanCarriesOver(t *testing.T) {
	store := attentionStore(t)
	// Yesterday's plan: a live task, a done task (dropped), a vanished id (dropped).
	attentionTask(t, store, "solo", TaskMeta{ID: "solo-9", Title: "done", Status: StatusDone, Created: dateAgo(1), Updated: stampAgo(time.Hour)})
	if err := WriteFocusPlan(store.Root, FocusPlan{Date: dateAgo(1), Tasks: []string{"solo-4", "solo-9", "ghost"}}); err != nil {
		t.Fatal(err)
	}
	a := build(t, store, "", AttentionOptions{})
	rows := a.Section(SectionFocus).Rows
	if len(rows) != 1 || rows[0].TaskID != "solo-4" || !has(rows[0].Flags, FlagStale) || !strings.Contains(rows[0].Reason, dateAgo(1)) {
		t.Fatalf("focus = %+v", rows)
	}
}

func TestAttentionLandedNoPR(t *testing.T) {
	a := build(t, attentionStore(t), "", AttentionOptions{})
	rows := a.Section(SectionLandedNoPR).Rows
	// Accepted parents: acme-api-2 (done), acme-zap-1 (partial); their merged
	// children carry no pr link. acme-api-3 is not accepted, so acme-api-3-1 is out.
	if got := ids(rows); strings.Join(got, ",") != "acme-api-2-1,acme-zap-1-1" {
		t.Fatalf("landed_no_pr = %v", got)
	}
	store := attentionStore(t)
	task, _ := store.FindTaskExact("acme-api", "acme-api-2-1")
	task.Meta.Links = map[string]string{"pr": "https://github.com/x/y/pull/1"}
	if err := store.WriteTask(task); err != nil {
		t.Fatal(err)
	}
	a = build(t, store, "", AttentionOptions{})
	if got := ids(a.Section(SectionLandedNoPR).Rows); strings.Join(got, ",") != "acme-zap-1-1" {
		t.Errorf("with a pr link = %v", got)
	}
}

// A tracker the human moved to done or archived leaves needs_me whatever
// its run-state files say, and its children leave landed_no_pr - the
// status is the decision, the files are only the machine's memory
// (orbit-108 on 2026-09-05: done in pm, still "acceptance failed"
// in the queue because of a finish.json from a month earlier).
func TestAttentionClosedTrackerLeavesQueue(t *testing.T) {
	store := attentionStore(t)
	close := func(id string, st TaskStatus) {
		task, err := store.FindTaskExact("acme-api", id)
		if err != nil {
			t.Fatal(err)
		}
		task.Meta.Status = st
		if err := store.WriteTask(task); err != nil {
			t.Fatal(err)
		}
	}
	close("acme-api-1", StatusDone)     // the failed run
	close("acme-api-2", StatusArchived) // the visual-claims acceptance (child acme-api-2-1 in landed_no_pr)
	a := build(t, store, "", AttentionOptions{})
	if got := ids(a.Section(SectionNeedsMe).Rows); strings.Join(got, ",") != "acme-api-3,acme-zap-1,acme-zap-2" {
		t.Errorf("needs_me with closed trackers = %v", got)
	}
	if got := ids(a.Section(SectionLandedNoPR).Rows); strings.Join(got, ",") != "acme-zap-1-1" {
		t.Errorf("landed_no_pr with an archived parent = %v", got)
	}
	// The sidebar's counters follow the queue: the archived tracker's two
	// visual claims and the done tracker's failed run no longer count.
	for _, g := range a.Groups {
		if g.Slug == "acme" && (g.Visual != 0 || g.Failed != 0) {
			t.Errorf("acme counters with closed trackers = visual %d failed %d, want 0/0", g.Visual, g.Failed)
		}
	}
	// A landing status is the executor's, not the human's: the row stays.
	close("acme-api-3", StatusMerged)
	a = build(t, store, "", AttentionOptions{})
	if got := ids(a.Section(SectionNeedsMe).Rows); !strings.Contains(strings.Join(got, ","), "acme-api-3") {
		t.Errorf("a merged tracker left needs_me: %v", got)
	}
}

func TestAttentionGroupsSumAcrossRepos(t *testing.T) {
	a := build(t, attentionStore(t), "", AttentionOptions{})
	var acme, solo *GroupSummary
	for i := range a.Groups {
		switch a.Groups[i].Slug {
		case "acme":
			acme = &a.Groups[i]
		case "solo":
			solo = &a.Groups[i]
		case "asleep":
			t.Fatal("an asleep project must not be a group")
		}
	}
	if acme == nil || solo == nil {
		t.Fatalf("groups = %+v", a.Groups)
	}
	if acme.Name != "ACME" || len(acme.Projects) != 2 || acme.Worst != SeverityCrit || acme.Failed != 1 || acme.Visual != 2 || acme.Waiting != 0 || acme.Quiet != 0 {
		t.Errorf("acme = %+v", *acme)
	}
	if solo.Worst != SeverityWarn || solo.Waiting != 3 || solo.Failed != 0 {
		t.Errorf("solo = %+v", *solo)
	}
	if a.Groups[0].Slug != "acme" {
		t.Errorf("worst group first: %v", a.Groups[0].Slug)
	}
	for _, g := range a.Groups {
		if (g.Slug == "silent" || g.Slug == "idle") && g.Quiet != 1 {
			t.Errorf("%s quiet = %d", g.Slug, g.Quiet)
		}
	}
	// solo-4 (touched yesterday) plus the five active tracker parents (doing,
	// touched 3h ago); solo-5 (10d) and the asleep project's tracker are out.
	if a.WIP != 6 {
		t.Errorf("wip = %d, want 6", a.WIP)
	}
}

func TestAttentionSectionsFollowConfig(t *testing.T) {
	a := build(t, attentionStore(t), "cockpit:\n  sections:\n    waiting: false\n    new_since_cutoff: true\n", AttentionOptions{})
	if a.Section(SectionWaiting) != nil {
		t.Error("a section switched off must not be emitted")
	}
	sec := a.Section(SectionNewSinceCutoff)
	if sec == nil {
		t.Fatal("new_since_cutoff switched on but absent")
	}
	// Created yesterday or today: solo-4 (1d), idle-2 (1d). dateAgo(2) is out.
	if got := ids(sec.Rows); !has(got, "solo-4") || !has(got, "idle-2") || has(got, "solo-2") {
		t.Errorf("new = %v", got)
	}
	// Default order: the seven default sections, in CockpitSections order.
	names := []string{}
	for _, s := range a.Sections {
		names = append(names, s.Name)
	}
	want := "needs_me,solo_reports,landed_no_pr,focus,in_progress,changes,stuck_projects,new_since_cutoff"
	if strings.Join(names, ",") != want {
		t.Errorf("sections = %v", names)
	}
}

func TestAttentionChangesIsEmbeddedNeverFetched(t *testing.T) {
	store := attentionStore(t)
	a := build(t, store, "", AttentionOptions{})
	sec := a.Section(SectionChanges)
	if sec == nil || len(sec.Rows) != 0 || sec.Note == "" || sec.Total != 0 {
		t.Fatalf("without a feed: %+v", sec)
	}
	digest := &ChangesDigest{Count: 7, Rows: []AttentionRow{{Section: SectionChanges, Project: "acme-api", Group: "acme", Title: "e1"}, {Section: SectionChanges, Project: "acme-api", Group: "acme", Title: "e2"}, {Section: SectionChanges, Title: "e3"}}}
	a = build(t, store, "", AttentionOptions{Changes: digest, ChangesRows: 2})
	sec = a.Section(SectionChanges)
	if sec.Total != 7 || len(sec.Rows) != 2 || sec.Note != "" {
		t.Fatalf("with a feed: %+v", sec)
	}
}

func TestAttentionScope(t *testing.T) {
	a := build(t, attentionStore(t), "", AttentionOptions{})
	g := a.Scope("", "acme")
	for _, s := range g.Sections {
		for _, r := range s.Rows {
			if r.Group != "acme" {
				t.Errorf("group scope leaked %+v", r)
			}
		}
	}
	if len(g.Groups) != 1 || g.Groups[0].Slug != "acme" {
		t.Errorf("group scope groups = %+v", g.Groups)
	}
	p := a.Scope("acme-zap", "")
	if got := ids(p.Section(SectionNeedsMe).Rows); strings.Join(got, ",") != "acme-zap-1,acme-zap-2" {
		t.Errorf("project scope needs_me = %v", got)
	}
	if p.Section(SectionNeedsMe).Total != 2 || len(p.Groups) != 1 || p.Groups[0].Slug != "acme" {
		t.Errorf("project scope: total=%d groups=%+v", p.Section(SectionNeedsMe).Total, p.Groups)
	}
	if p.WIP != a.WIP {
		t.Errorf("wip is a global header number and must survive scoping")
	}
}

func TestCutoffSince(t *testing.T) {
	loc := time.Local
	before := time.Date(2026, 9, 5, 17, 59, 0, 0, loc)
	after := time.Date(2026, 9, 5, 18, 0, 0, 0, loc)
	if got := CutoffSince(18, before); got != time.Date(2026, 9, 4, 18, 0, 0, 0, loc) {
		t.Errorf("before cutoff: %v", got)
	}
	if got := CutoffSince(18, after); got != time.Date(2026, 9, 5, 18, 0, 0, 0, loc) {
		t.Errorf("at cutoff: %v", got)
	}
	// Across midnight: 00:30 is before 18:00, so yesterday 18:00.
	if got := CutoffSince(18, time.Date(2026, 9, 6, 0, 30, 0, 0, loc)); got != time.Date(2026, 9, 5, 18, 0, 0, 0, loc) {
		t.Errorf("after midnight: %v", got)
	}
}

func TestHumanAge(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "30s", 5 * time.Minute: "5m", 3 * time.Hour: "3h",
		47 * time.Hour: "47h", 49 * time.Hour: "2d", -time.Second: "0s",
	}
	for d, want := range cases {
		if got := HumanAge(d); got != want {
			t.Errorf("HumanAge(%v) = %q, want %q", d, got, want)
		}
	}
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
