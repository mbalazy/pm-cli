package storage

import (
	"os"
	"testing"
	"time"
)

// runsStore builds a synthetic pm root with two projects, so the aggregation is
// exercised the way it is used: across projects, not inside one.
func runsStore(t *testing.T) *Store {
	t.Helper()
	store := &Store{Root: t.TempDir()}
	for _, slug := range []string{"alpha", "beta"} {
		if err := store.CreateProject(slug, &Project{Name: slug, Prefix: slug, Path: t.TempDir()}); err != nil {
			t.Fatalf("create project %s: %v", slug, err)
		}
	}
	return store
}

func runsTask(t *testing.T, store *Store, slug string, meta TaskMeta) {
	t.Helper()
	if err := store.AddTask(slug, &Task{Meta: meta}); err != nil {
		t.Fatalf("add task %s: %v", meta.ID, err)
	}
}

// tracker plus one child - the minimum that makes BuildTrackers emit a row.
func runsTracker(t *testing.T, store *Store, slug, id, title, updated string) {
	t.Helper()
	runsTask(t, store, slug, TaskMeta{ID: id, Title: title, Status: StatusDoing, Created: updated, Updated: updated})
	runsTask(t, store, slug, TaskMeta{ID: id + "-1", Title: "child", Status: StatusTodo, Created: updated, Updated: updated, Parent: id})
}

func rowFor(rows []RunRow, tracker string) *RunRow {
	for i := range rows {
		if rows[i].Tracker == tracker {
			return &rows[i]
		}
	}
	return nil
}

func TestLocalRunRowsAcrossProjects(t *testing.T) {
	store := runsStore(t)
	runsTracker(t, store, "alpha", "alpha-1", "has a run", "2026-08-01")
	runsTracker(t, store, "alpha", "alpha-2", "never run", "2026-08-02")
	runsTracker(t, store, "beta", "beta-1", "accepted", "2026-08-03")
	// A childless task is not a tracker and must not produce a row.
	runsTask(t, store, "beta", TaskMeta{ID: "beta-9", Title: "standalone", Status: StatusDoing, Created: "2026-08-04", Updated: "2026-08-04"})

	alphaDir := store.ProjectDir("alpha")
	if err := WriteRunState(alphaDir, &RunState{
		TaskID: "alpha-1", Project: "alpha", Kind: RunKindEpic, Status: RunStatusRunning,
		PID: os.Getpid(), Started: time.Now().UTC().Format(time.RFC3339),
		Subs: []SubRun{
			{ID: "alpha-1-1", Status: "merged"},
			{ID: "alpha-1-2", Status: RunStatusRunning},
			{ID: "alpha-1-3", Status: "pending"},
		},
	}); err != nil {
		t.Fatalf("write run state: %v", err)
	}

	betaDir := store.ProjectDir("beta")
	if err := WriteRunState(betaDir, &RunState{
		TaskID: "beta-1", Project: "beta", Kind: RunKindEpic, Status: RunStatusDone,
		PID: os.Getpid(), Started: "2026-08-03T10:00:00Z",
		Subs: []SubRun{{ID: "beta-1-1", Status: "pushed"}},
	}); err != nil {
		t.Fatalf("write run state: %v", err)
	}
	// A FINISHED acceptance of that run, carrying open visual claims.
	if err := WriteRunState(betaDir, &RunState{
		TaskID: "beta-1", Project: "beta", Kind: RunKindFinish, Status: RunStatusDone,
		PID: os.Getpid(), Started: "2026-08-03T11:00:00Z",
		Subs: []SubRun{{ID: "beta-1", Status: "done", VisualClaimsOpen: 4}},
	}); err != nil {
		t.Fatalf("write finish state: %v", err)
	}

	rows, err := LocalRunRows(store, nil)
	if err != nil {
		t.Fatalf("LocalRunRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 tracker rows, got %d: %+v", len(rows), rows)
	}
	if rowFor(rows, "beta-9") != nil {
		t.Error("a childless task must not appear in the runs list")
	}

	// A live run: counted over the subs it is FINISHED with.
	if got := rowFor(rows, "alpha-1").Run.String(); got != "running 1/3" {
		t.Errorf("alpha-1 RUN = %q, want %q", got, "running 1/3")
	}
	// No run-state at all.
	if got := rowFor(rows, "alpha-2").Run.String(); got != RunCellPrepped {
		t.Errorf("alpha-2 RUN = %q, want %q", got, RunCellPrepped)
	}
	if got := rowFor(rows, "alpha-2").Accept.String(); got != "-" {
		t.Errorf("alpha-2 ACCEPTANCE = %q, want %q", got, "-")
	}
	beta := rowFor(rows, "beta-1")
	if got := beta.Run.String(); got != "done 1/1" {
		t.Errorf("beta-1 RUN = %q, want %q", got, "done 1/1")
	}
	// The open visual claims ride along with a DONE acceptance - that is the
	// case they exist for.
	if beta.Accept.VisualClaimsOpen != 4 || beta.Accept.String() != "done, 4 visual claim(s) open" {
		t.Errorf("beta-1 ACCEPTANCE = %q (%d open), want the done row to carry 4 open claims",
			beta.Accept.String(), beta.Accept.VisualClaimsOpen)
	}
	if beta.Project != "beta" || beta.Remote != "" {
		t.Errorf("beta-1 row = project %q remote %q, want a local beta row", beta.Project, beta.Remote)
	}
}

// A run-state is a file that outlives its process: a manager killed with -9
// leaves `running` behind, and reporting that as running would promise a run
// that is not there.
func TestLocalRunRowsDeadPIDIsStale(t *testing.T) {
	store := runsStore(t)
	runsTracker(t, store, "alpha", "alpha-1", "crashed", "2026-08-01")
	if err := WriteRunState(store.ProjectDir("alpha"), &RunState{
		TaskID: "alpha-1", Project: "alpha", Kind: RunKindEpic, Status: RunStatusRunning,
		PID: deadPID, Started: time.Now().UTC().Format(time.RFC3339),
		Subs: []SubRun{{ID: "alpha-1-1", Status: "merged"}, {ID: "alpha-1-2", Status: "pending"}},
	}); err != nil {
		t.Fatalf("write run state: %v", err)
	}

	rows, err := LocalRunRows(store, []string{"alpha"})
	if err != nil {
		t.Fatalf("LocalRunRows: %v", err)
	}
	cell := rowFor(rows, "alpha-1").Run
	if cell.State != RunCellStale {
		t.Errorf("RUN state = %q, want %q", cell.State, RunCellStale)
	}
	// The counts survive the demotion - they are what says how far it got.
	if cell.Done != 1 || cell.Total != 2 {
		t.Errorf("stale run lost its counts: %d/%d, want 1/2", cell.Done, cell.Total)
	}
	if got := cell.String(); got != RunCellStale {
		t.Errorf("stale renders as %q, want the bare word", got)
	}
}

// A live claim outranks the acceptance run-state: the acceptance may be running
// on another machine, where its run-state's pid means nothing here.
func TestLocalRunRowsLiveClaimWins(t *testing.T) {
	store := runsStore(t)
	runsTracker(t, store, "alpha", "alpha-1", "being accepted", "2026-08-01")
	dir := store.ProjectDir("alpha")
	if err := WriteRunState(dir, &RunState{
		TaskID: "alpha-1", Project: "alpha", Kind: RunKindFinish, Status: RunStatusDone,
		PID: os.Getpid(), Started: "2026-08-01T10:00:00Z",
		Subs: []SubRun{{ID: "alpha-1", Status: "done", VisualClaimsOpen: 2}},
	}); err != nil {
		t.Fatalf("write finish state: %v", err)
	}
	if _, err := AcquireFinishClaim(dir, "alpha-1", "sess-1"); err != nil {
		t.Fatalf("acquire claim: %v", err)
	}

	rows, err := LocalRunRows(store, []string{"alpha"})
	if err != nil {
		t.Fatalf("LocalRunRows: %v", err)
	}
	cell := rowFor(rows, "alpha-1").Accept
	if cell.State != AcceptCellRunning {
		t.Errorf("ACCEPTANCE state = %q, want %q (a live claim outranks a done run-state)", cell.State, AcceptCellRunning)
	}
	if cell.Host != Hostname() {
		t.Errorf("ACCEPTANCE host = %q, want the claim holder %q", cell.Host, Hostname())
	}
	if cell.Age == "" || cell.Age == "unknown" {
		t.Errorf("ACCEPTANCE age = %q, want an elapsed time", cell.Age)
	}
	// The claim says who is on it; the count of unsettled visual work is still
	// the tracker's, so it survives.
	if cell.VisualClaimsOpen != 2 {
		t.Errorf("open visual claims = %d, want 2", cell.VisualClaimsOpen)
	}
}

// An acceptance whose process is gone but whose state still says running.
func TestLocalRunRowsAcceptanceStale(t *testing.T) {
	store := runsStore(t)
	runsTracker(t, store, "alpha", "alpha-1", "killed acceptance", "2026-08-01")
	if err := WriteRunState(store.ProjectDir("alpha"), &RunState{
		TaskID: "alpha-1", Project: "alpha", Kind: RunKindFinish, Status: RunStatusRunning,
		PID: deadPID, Started: time.Now().UTC().Format(time.RFC3339),
		Subs: []SubRun{{ID: "alpha-1", Status: RunStatusRunning}},
	}); err != nil {
		t.Fatalf("write finish state: %v", err)
	}

	rows, err := LocalRunRows(store, []string{"alpha"})
	if err != nil {
		t.Fatalf("LocalRunRows: %v", err)
	}
	if got := rowFor(rows, "alpha-1").Accept.String(); got != AcceptCellStale {
		t.Errorf("ACCEPTANCE = %q, want %q", got, AcceptCellStale)
	}
	// The RUN column is untouched by the acceptance's state - the two runs share
	// a task id and must not share a cell.
	if got := rowFor(rows, "alpha-1").Run.String(); got != RunCellPrepped {
		t.Errorf("RUN = %q, want %q - the acceptance state must not leak into it", got, RunCellPrepped)
	}
}

// The visual-claim total is a SUM across the acceptance's subs, not the first
// one it happens to find.
func TestAcceptCellSumsVisualClaims(t *testing.T) {
	store := runsStore(t)
	runsTracker(t, store, "alpha", "alpha-1", "many subs", "2026-08-01")
	if err := WriteRunState(store.ProjectDir("alpha"), &RunState{
		TaskID: "alpha-1", Project: "alpha", Kind: RunKindFinish, Status: RunStatusDone,
		PID: os.Getpid(), Started: "2026-08-01T10:00:00Z",
		Subs: []SubRun{
			{ID: "a", Status: "done", VisualClaimsOpen: 0},
			{ID: "b", Status: "done", VisualClaimsOpen: 2},
			{ID: "c", Status: "done", VisualClaimsOpen: 3},
		},
	}); err != nil {
		t.Fatalf("write finish state: %v", err)
	}

	rows, err := LocalRunRows(store, []string{"alpha"})
	if err != nil {
		t.Fatalf("LocalRunRows: %v", err)
	}
	if got := rowFor(rows, "alpha-1").Accept.VisualClaimsOpen; got != 5 {
		t.Errorf("open visual claims = %d, want 5 (2+3)", got)
	}
}

// Newest activity first, and the activity is the newest of the tracker's own
// stamp, its run's and its acceptance's - not just the task file's.
func TestSortRunRowsNewestFirst(t *testing.T) {
	store := runsStore(t)
	runsTracker(t, store, "alpha", "alpha-1", "old task, fresh run", "2026-01-01")
	runsTracker(t, store, "alpha", "alpha-2", "recent task, no run", "2026-08-05")
	if err := WriteRunState(store.ProjectDir("alpha"), &RunState{
		TaskID: "alpha-1", Project: "alpha", Kind: RunKindEpic, Status: RunStatusDone,
		PID: os.Getpid(), Started: "2026-08-07T10:00:00Z",
		Subs: []SubRun{{ID: "alpha-1-1", Status: "merged"}},
	}); err != nil {
		t.Fatalf("write run state: %v", err)
	}

	rows, err := LocalRunRows(store, []string{"alpha"})
	if err != nil {
		t.Fatalf("LocalRunRows: %v", err)
	}
	if rows[0].Tracker != "alpha-1" {
		t.Errorf("first row = %s, want alpha-1 (its RUN is the freshest activity)", rows[0].Tracker)
	}

	// A row with no parsable stamp (a note placeholder) sorts last, never first.
	withNote := append([]RunRow{{Remote: "runner", Project: "runner", Note: "unreachable"}}, rows...)
	SortRunRows(withNote)
	if withNote[len(withNote)-1].Note == "" {
		t.Errorf("a stamp-less note row must sort last, got order %+v", withNote)
	}
}

func TestRunCellStrings(t *testing.T) {
	for _, tc := range []struct {
		name string
		cell RunCell
		want string
	}{
		{"empty", RunCell{}, "-"},
		{"prepped", RunCell{State: RunCellPrepped}, "prepped"},
		{"running", RunCell{State: RunCellRunning, Done: 3, Total: 8}, "running 3/8"},
		{"done", RunCell{State: RunCellDone, Done: 6, Total: 6}, "done 6/6"},
		{"failed keeps no counts", RunCell{State: RunCellFailed, Done: 1, Total: 5}, "failed"},
		{"stale keeps no counts", RunCell{State: RunCellStale, Done: 1, Total: 5}, "stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cell.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAcceptCellStrings(t *testing.T) {
	for _, tc := range []struct {
		name string
		cell AcceptCell
		want string
	}{
		{"none", AcceptCell{}, "-"},
		{"running remote", AcceptCell{State: AcceptCellRunning, Host: "runner", Age: "22m0s"}, "running (runner, 22m0s)"},
		{"running local", AcceptCell{State: AcceptCellRunning, Age: "3m0s"}, "running (3m0s)"},
		{"done clean", AcceptCell{State: AcceptCellDone}, "done"},
		{"done with claims", AcceptCell{State: AcceptCellDone, VisualClaimsOpen: 4}, "done, 4 visual claim(s) open"},
		{"failed", AcceptCell{State: AcceptCellFailed}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cell.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A project whose tasks cannot be read is a broken local store, not the
// unreachable-machine case - it must fail rather than silently drop rows.
func TestLocalRunRowsUnknownProjectErrors(t *testing.T) {
	store := runsStore(t)
	if _, err := LocalRunRows(store, []string{"nope"}); err == nil {
		t.Error("want an error for a project that does not exist")
	}
}
