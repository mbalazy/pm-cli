package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// lockFailStore wraps a real TaskStore and forces LockProject to fail, so
// tests can exercise the degrade-to-unlocked-write path without racing a
// real lock failure.
type lockFailStore struct {
	storage.TaskStore
	err error
}

func (s *lockFailStore) LockProject(slug string) (func(), error) {
	return nil, s.err
}

// captureStderr swaps os.Stderr for a pipe, runs fn, and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return capturePipe(t, &os.Stderr, fn)
}

// capturePipe points *target at a pipe for the duration of fn and returns what
// fn wrote. The pipe is drained by a concurrent reader: a fixture writing more
// than the OS pipe buffer (~64KB) would otherwise block fn forever and hang the
// whole test binary instead of failing an assertion. Restore + close go through
// a deferred sync.Once so a t.Fatal or panic inside fn cannot leave the stream
// pointing at a dead pipe for every later test in the package.
func capturePipe(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	old := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	drained := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		r.Close()
		drained <- string(out)
	}()

	var once sync.Once
	restore := func() {
		once.Do(func() {
			*target = old
			w.Close()
		})
	}
	*target = w
	defer restore()
	fn()
	restore()
	return <-drained
}

func TestCapturePipeSurvivesMoreThanThePipeBuffer(t *testing.T) {
	big := strings.Repeat("x", 200*1024) // > the ~64KB OS pipe buffer
	if got := captureStdout(t, func() { fmt.Print(big) }); got != big {
		t.Fatalf("large capture: got %d bytes, want %d", len(got), len(big))
	}
}

func TestLogIfErr(t *testing.T) {
	if got := captureStderr(t, func() { logIfErr(os.Stderr, "move x-1 to doing", nil) }); got != "" {
		t.Errorf("nil err should print nothing, got %q", got)
	}
	got := captureStderr(t, func() { logIfErr(os.Stderr, "move x-1 to doing", errors.New("disk full")) })
	if !strings.Contains(got, "move x-1 to doing") || !strings.Contains(got, "disk full") {
		t.Errorf("expected context + error in output, got %q", got)
	}
}

// tempStore creates an isolated store with one project ("proj") whose path is a
// temp dir, and returns the store + slug.
func tempStore(t *testing.T) (*storage.Store, string) {
	t.Helper()
	root := t.TempDir()
	store := &storage.Store{Root: root}
	if err := store.CreateProject("proj", &storage.Project{Name: "Proj", Path: t.TempDir()}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return store, "proj"
}

func addTask(t *testing.T, store *storage.Store, slug string, meta storage.TaskMeta, body string) *storage.Task {
	t.Helper()
	task := &storage.Task{Meta: meta, Body: body}
	if err := store.AddTask(slug, task); err != nil {
		t.Fatalf("add task %s: %v", meta.ID, err)
	}
	return task
}

func TestReadySubs(t *testing.T) {
	store, slug := tempStore(t)
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1", Title: "Tracker", Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-2", Title: "B", Status: storage.StatusTodo, Parent: "proj-1", Order: 2}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-1", Title: "A", Status: storage.StatusTodo, Parent: "proj-1", Order: 1}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-3", Title: "C", Status: storage.StatusTodo, Parent: "proj-1", Order: 3}, "")
	// unrelated task with a different parent must be excluded
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-2", Title: "Other", Status: storage.StatusTodo, Parent: "proj-9"}, "")

	subs, err := readySubs(store, slug, "proj-1")
	if err != nil {
		t.Fatalf("readySubs: %v", err)
	}
	got := make([]string, len(subs))
	for i, s := range subs {
		got[i] = s.Meta.ID
	}
	want := []string{"proj-1-1", "proj-1-2", "proj-1-3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("readySubs order = %v, want %v", got, want)
	}
}

func TestRecordSubFeedback(t *testing.T) {
	store, slug := tempStore(t)

	t.Run("appends Manager Notes section into the spec", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-10", Title: "Epic", Status: storage.StatusDoing},
			storage.SpecStart+"\n## Description\nbuild it\n"+storage.SpecEnd+"\n\nlog tail")

		if err := recordSubFeedback(store, parent, "proj-10-1", "merged", []string{"streak store seam"}, io.Discard); err != nil {
			t.Fatalf("recordSubFeedback: %v", err)
		}
		// second finding from a later sub accumulates
		if err := recordSubFeedback(store, parent, "proj-10-2", "merged", []string{"nav handoff"}, io.Discard); err != nil {
			t.Fatalf("recordSubFeedback 2: %v", err)
		}

		reloaded, err := store.FindTask(slug, "proj-10")
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		spec := storage.ExtractSpec(reloaded.Body)
		mustContain(t, spec, managerNotesHeading)
		mustContain(t, spec, "[proj-10-1] streak store seam")
		mustContain(t, spec, "[proj-10-2] nav handoff")
		mustContain(t, spec, "## Description")
		// only one Manager Notes heading despite two appends
		if c := strings.Count(spec, managerNotesHeading); c != 1 {
			t.Errorf("expected 1 Manager Notes heading, got %d", c)
		}
		// log tail outside the spec preserved
		mustContain(t, reloaded.Body, "log tail")
	})

	t.Run("tags a parked sub and refreshes it in place on re-run", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-12", Title: "Epic3", Status: storage.StatusDoing},
			storage.SpecStart+"\n## Description\nbuild it\n"+storage.SpecEnd)

		if err := recordSubFeedback(store, parent, "proj-12-2", "blocked", []string{"needs API field X"}, io.Discard); err != nil {
			t.Fatalf("record blocked: %v", err)
		}
		reloaded, _ := store.FindTask(slug, "proj-12")
		spec := storage.ExtractSpec(reloaded.Body)
		mustContain(t, spec, "[proj-12-2 · blocked] needs API field X")

		// Re-run: same sub, fresh reason -> the old line is replaced, not stacked.
		if err := recordSubFeedback(store, reloaded, "proj-12-2", "blocked", []string{"still needs API field X (v2)"}, io.Discard); err != nil {
			t.Fatalf("record blocked 2: %v", err)
		}
		reloaded2, _ := store.FindTask(slug, "proj-12")
		spec2 := storage.ExtractSpec(reloaded2.Body)
		mustContain(t, spec2, "[proj-12-2 · blocked] still needs API field X (v2)")
		if strings.Contains(spec2, "needs API field X\n") || strings.Count(spec2, "proj-12-2") != 1 {
			t.Errorf("expected exactly one proj-12-2 line after re-run, got:\n%s", spec2)
		}
	})

	t.Run("a shared-prefix sub id is not clobbered", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-13", Title: "Epic4", Status: storage.StatusDoing},
			storage.SpecStart+"\n## Description\nx\n"+storage.SpecEnd)
		_ = recordSubFeedback(store, parent, "proj-13-1", "blocked", []string{"one"}, io.Discard)
		reloaded, _ := store.FindTask(slug, "proj-13")
		_ = recordSubFeedback(store, reloaded, "proj-13-12", "blocked", []string{"twelve"}, io.Discard)
		// refreshing proj-13-1 must leave proj-13-12 intact
		reloaded2, _ := store.FindTask(slug, "proj-13")
		_ = recordSubFeedback(store, reloaded2, "proj-13-1", "blocked", []string{"one-again"}, io.Discard)
		final, _ := store.FindTask(slug, "proj-13")
		spec := storage.ExtractSpec(final.Body)
		mustContain(t, spec, "[proj-13-1 · blocked] one-again")
		mustContain(t, spec, "[proj-13-12 · blocked] twelve")
	})

	t.Run("falls back to Log when parent has no spec", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-11", Title: "Epic2", Status: storage.StatusDoing}, "plain body, no markers")
		if err := recordSubFeedback(store, parent, "proj-11-1", "merged", []string{"finding x"}, io.Discard); err != nil {
			t.Fatalf("recordSubFeedback: %v", err)
		}
		reloaded, _ := store.FindTask(slug, "proj-11")
		if storage.ExtractSpec(reloaded.Body) != "" {
			t.Error("did not expect a spec block to be created")
		}
		mustContain(t, reloaded.Body, "[proj-11-1] finding x")
		mustContain(t, reloaded.Body, "plain body, no markers")
	})

	t.Run("degrades to an unlocked write and warns on errOut when the lock fails", func(t *testing.T) {
		parent := addTask(t, store, slug, storage.TaskMeta{ID: "proj-14", Title: "Epic5", Status: storage.StatusDoing},
			storage.SpecStart+"\n## Description\nx\n"+storage.SpecEnd)

		lockErr := errors.New("boom: permission denied")
		failing := &lockFailStore{TaskStore: store, err: lockErr}

		var errOut bytes.Buffer
		if err := recordSubFeedback(failing, parent, "proj-14-1", "merged", []string{"finding"}, &errOut); err != nil {
			t.Fatalf("recordSubFeedback degraded to unlocked but still failed: %v", err)
		}

		reloaded, _ := store.FindTask(slug, "proj-14")
		mustContain(t, storage.ExtractSpec(reloaded.Body), "[proj-14-1] finding")

		want := fmt.Sprintf("pm run-epic: project lock unavailable for %q (%v) - recording feedback for proj-14-1 without it\n", slug, lockErr)
		if errOut.String() != want {
			t.Fatalf("warning = %q, want %q", errOut.String(), want)
		}
	})
}

func TestParkedFindings(t *testing.T) {
	if got := parkedFindings(&workerResult{Unresolved: []string{"q1", "q2"}}); strings.Join(got, ",") != "q1,q2" {
		t.Errorf("unresolved should win: %v", got)
	}
	if got := parkedFindings(&workerResult{Summary: "tests red"}); strings.Join(got, ",") != "tests red" {
		t.Errorf("summary fallback: %v", got)
	}
	if got := parkedFindings(&workerResult{}); strings.Join(got, ",") != "(no detail)" {
		t.Errorf("empty fallback: %v", got)
	}
}

func TestManagerNoteBlock(t *testing.T) {
	if got := managerNoteBlock("s-1", "merged", []string{"a", "b"}); got != "- [s-1] a\n- [s-1] b" {
		t.Errorf("merged tag: got %q", got)
	}
	if got := managerNoteBlock("s-1", "blocked", []string{"a"}); got != "- [s-1 · blocked] a" {
		t.Errorf("blocked tag: got %q", got)
	}
}

func TestUnmetDeps(t *testing.T) {
	done := storage.TaskStatus("merged") // doneStatus for the epic flow
	mk := func(id string, status storage.TaskStatus, deps ...string) *storage.Task {
		return &storage.Task{Meta: storage.TaskMeta{ID: id, Status: status, DependsOn: deps}}
	}
	x1 := mk("x-1", "merged")
	x1todo := mk("x-1", storage.StatusTodo)
	x1done := mk("x-1", storage.StatusDone)
	byID := func(subs ...*storage.Task) map[string]*storage.Task {
		m := map[string]*storage.Task{}
		for _, s := range subs {
			m[s.Meta.ID] = s
		}
		return m
	}

	t.Run("no deps -> satisfied", func(t *testing.T) {
		if r := unmetDeps(mk("x-3", storage.StatusTodo), byID(), done); r != "" {
			t.Errorf("expected satisfied, got %q", r)
		}
	})
	t.Run("dep merged -> satisfied", func(t *testing.T) {
		sub := mk("x-3", storage.StatusTodo, "x-1")
		if r := unmetDeps(sub, byID(x1), done); r != "" {
			t.Errorf("merged dep should satisfy, got %q", r)
		}
	})
	t.Run("dep done -> satisfied", func(t *testing.T) {
		sub := mk("x-3", storage.StatusTodo, "x-1")
		if r := unmetDeps(sub, byID(x1done), done); r != "" {
			t.Errorf("done dep should satisfy, got %q", r)
		}
	})
	t.Run("dep not satisfied -> reason names it + status", func(t *testing.T) {
		sub := mk("x-3", storage.StatusTodo, "x-1")
		r := unmetDeps(sub, byID(x1todo), done)
		if !strings.Contains(r, "x-1") || !strings.Contains(r, "todo") {
			t.Errorf("reason should name the unmet dep and its status, got %q", r)
		}
	})
	t.Run("unknown dep -> unmet", func(t *testing.T) {
		sub := mk("x-3", storage.StatusTodo, "x-9")
		if r := unmetDeps(sub, byID(), done); !strings.Contains(r, "x-9") || !strings.Contains(r, "unknown") {
			t.Errorf("unknown dep should be unmet, got %q", r)
		}
	})
	t.Run("multiple deps, one unmet -> reported", func(t *testing.T) {
		sub := mk("x-3", storage.StatusTodo, "x-1", "x-2")
		x2 := mk("x-2", storage.StatusWaiting)
		r := unmetDeps(sub, byID(x1, x2), done)
		if strings.Contains(r, "x-1") {
			t.Errorf("satisfied dep should not appear, got %q", r)
		}
		if !strings.Contains(r, "x-2") {
			t.Errorf("unmet dep x-2 should be reported, got %q", r)
		}
	})
}

func TestClassifySub(t *testing.T) {
	const start, done = storage.TaskStatus("todo"), storage.TaskStatus("merged")
	mk := func(id string, status storage.TaskStatus, mode string, deps ...string) *storage.Task {
		return &storage.Task{Meta: storage.TaskMeta{ID: id, Status: status, Mode: mode, DependsOn: deps}}
	}
	byID := func(subs ...*storage.Task) map[string]*storage.Task {
		m := map[string]*storage.Task{}
		for _, s := range subs {
			m[s.Meta.ID] = s
		}
		return m
	}

	t.Run("ready auto sub is driven", func(t *testing.T) {
		_, drive, _ := classifySub(mk("x-1", start, ""), byID(), start, done, nil)
		if !drive {
			t.Error("ready auto sub should be driven")
		}
	})

	t.Run("manual sub is never driven even when otherwise ready", func(t *testing.T) {
		// status == start, no unmet deps -> would be READY if it were auto.
		sub := mk("x-1", start, "manual")
		oc, drive, announce := classifySub(sub, byID(), start, done, nil)
		if drive {
			t.Fatal("manual sub must NOT be driven (no worker)")
		}
		if oc.result != "manual" {
			t.Errorf("manual outcome should be %q, not %q (distinct from skipped)", "manual", oc.result)
		}
		if !strings.Contains(announce, "manual") {
			t.Errorf("expected a manual stderr announcement, got %q", announce)
		}
		// classifySub is pure: the sub's status must be untouched.
		if sub.Meta.Status != start {
			t.Errorf("manual sub status changed to %q - must stay untouched", sub.Meta.Status)
		}
	})

	t.Run("manual sub with satisfied deps is still not driven", func(t *testing.T) {
		dep := mk("x-1", done, "")
		sub := mk("x-2", start, "manual", "x-1")
		oc, drive, _ := classifySub(sub, byID(dep), start, done, nil)
		if drive || oc.result != "manual" {
			t.Errorf("manual sub with met deps: drive=%v result=%q, want drive=false result=manual", drive, oc.result)
		}
	})

	t.Run("manual sub already at done reports as skipped/done, not manual", func(t *testing.T) {
		oc, drive, _ := classifySub(mk("x-1", done, "manual"), byID(), start, done, nil)
		if drive {
			t.Error("done sub should not be driven")
		}
		if oc.result != "skipped" {
			t.Errorf("a completed manual sub should report %q (done handling wins), got %q", "skipped", oc.result)
		}
	})

	t.Run("downstream auto sub stays blocked until the manual dep is moved to done", func(t *testing.T) {
		manual := mk("x-1", start, "manual") // human hasn't done it yet
		downstream := mk("x-2", start, "", "x-1")
		m := byID(manual, downstream)

		// While the manual sub is not done, the downstream sub is gated (unmet dep)
		// and not driven.
		oc, drive, _ := classifySub(downstream, m, start, done, nil)
		if drive {
			t.Fatal("downstream sub must stay blocked while the manual dep is unmet")
		}
		if oc.result != "skipped" || !strings.Contains(oc.note, "x-1") {
			t.Errorf("downstream should be skipped waiting on x-1, got result=%q note=%q", oc.result, oc.note)
		}

		// Human does the work and moves the manual sub to done externally.
		manual.Meta.Status = done
		if _, drive, _ := classifySub(downstream, m, start, done, nil); !drive {
			t.Error("downstream sub should be driven once the manual dep is done")
		}
	})

	t.Run("doing sub of a crashed run is recovered, loudly", func(t *testing.T) {
		sub := mk("x-1", storage.StatusDoing, "")
		oc, drive, announce := classifySub(sub, byID(), start, done, map[string]bool{"x-1": true})
		if !drive {
			t.Fatal("a crash-recovered doing sub must be driven, not skipped as not ready")
		}
		if oc.id != "x-1" {
			t.Errorf("outcome id = %q", oc.id)
		}
		if !strings.Contains(announce, "recovered after crash") {
			t.Errorf("recovery must be announced, got %q", announce)
		}
	})

	t.Run("doing sub NOT owned by the crashed run stays not-ready", func(t *testing.T) {
		// e.g. a sub a human is working on right now, unrelated to the dead run.
		oc, drive, _ := classifySub(mk("x-2", storage.StatusDoing, ""), byID(), start, done, map[string]bool{"x-1": true})
		if drive || !strings.Contains(oc.note, "not ready") {
			t.Errorf("drive=%v note=%q, want a plain not-ready skip", drive, oc.note)
		}
	})

	t.Run("recovery applies to the doing signature only", func(t *testing.T) {
		// A sub the crashed run touched but that has since moved elsewhere
		// (waiting, a custom status) is NOT the crash signature - leave it.
		oc, drive, _ := classifySub(mk("x-1", storage.StatusWaiting, ""), byID(), start, done, map[string]bool{"x-1": true})
		if drive || !strings.Contains(oc.note, "not ready") {
			t.Errorf("drive=%v note=%q, want a not-ready skip for a non-doing status", drive, oc.note)
		}
	})

	t.Run("a recovered sub still passes the depends_on gate", func(t *testing.T) {
		dep := mk("x-1", storage.StatusWaiting, "")
		sub := mk("x-2", storage.StatusDoing, "", "x-1")
		oc, drive, _ := classifySub(sub, byID(dep), start, done, map[string]bool{"x-2": true})
		if drive || !strings.Contains(oc.note, "x-1") {
			t.Errorf("drive=%v note=%q, want an unmet-dep skip even for a recovered sub", drive, oc.note)
		}
	})
}

// deadPID is high enough to be unused on both platforms pm runs on (same
// constant the storage process tests use).
const deadPID = 2147483646

func TestCrashRecoveredSubs(t *testing.T) {
	write := func(t *testing.T, dir string, st *storage.RunState) {
		t.Helper()
		if err := storage.WriteRunState(dir, st); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("dead running manager -> its mid-flight subs are recoverable", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, &storage.RunState{
			TaskID: "app-1", Project: "app", Kind: storage.RunKindEpic,
			Status: storage.RunStatusRunning, PID: deadPID,
			Started:    "2026-01-01T00:00:00Z",
			CurrentSub: "app-1-2",
			Subs: []storage.SubRun{
				{ID: "app-1-1", Status: "merged"},
				{ID: "app-1-2", Status: storage.RunStatusRunning},
				{ID: "app-1-3", Status: "pending"},
			},
		})
		rec := crashRecoveredSubs(dir, "app-1")
		if !rec["app-1-2"] {
			t.Error("the sub the dead run was driving must be recoverable")
		}
		if rec["app-1-1"] || rec["app-1-3"] {
			t.Errorf("finished/never-started subs must not be marked, got %v", rec)
		}
	})

	t.Run("live running manager -> nothing to recover", func(t *testing.T) {
		dir := t.TempDir()
		// The stamp must postdate this process's start: a stamp OLDER than the
		// start time reads as a recycled pid (correctly - see ProcessAliveSince).
		write(t, dir, &storage.RunState{
			TaskID: "app-1", Project: "app", Kind: storage.RunKindEpic,
			Status: storage.RunStatusRunning, PID: os.Getpid(),
			Started: time.Now().UTC().Format(time.RFC3339), CurrentSub: "app-1-2",
			Subs: []storage.SubRun{{ID: "app-1-2", Status: storage.RunStatusRunning}},
		})
		if rec := crashRecoveredSubs(dir, "app-1"); rec != nil {
			t.Errorf("a LIVE run's subs must never be stolen, got %v", rec)
		}
	})

	t.Run("finished previous run -> nothing to recover", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, &storage.RunState{
			TaskID: "app-1", Project: "app", Kind: storage.RunKindEpic,
			Status: storage.RunStatusDone, PID: deadPID,
			Started: "2026-01-01T00:00:00Z",
			Subs:    []storage.SubRun{{ID: "app-1-2", Status: "merged"}},
		})
		if rec := crashRecoveredSubs(dir, "app-1"); rec != nil {
			t.Errorf("a completed run leaves nothing to recover, got %v", rec)
		}
	})

	t.Run("no prior run-state -> nothing to recover", func(t *testing.T) {
		if rec := crashRecoveredSubs(t.TempDir(), "app-1"); rec != nil {
			t.Errorf("no run-state must mean no recovery, got %v", rec)
		}
	})
}

func TestValidateModeRejectedOnWrite(t *testing.T) {
	store, slug := tempStore(t)
	err := store.AddTask(slug, &storage.Task{
		Meta: storage.TaskMeta{ID: "proj-1", Title: "bad mode", Status: storage.StatusTodo, Mode: "sometimes"},
	})
	if err == nil {
		t.Fatal("expected AddTask to reject an invalid mode")
	}
	if !strings.Contains(err.Error(), "mode") {
		t.Errorf("error should mention mode, got %q", err.Error())
	}
	// valid values (and empty) round-trip.
	for _, m := range []string{"", "auto", "manual"} {
		id := "proj-ok-" + m
		if m == "" {
			id = "proj-ok-empty"
		}
		if err := store.AddTask(slug, &storage.Task{
			Meta: storage.TaskMeta{ID: id, Title: id, Status: storage.StatusTodo, Mode: m},
		}); err != nil {
			t.Errorf("mode %q should be valid, got %v", m, err)
		}
	}
	reloaded, err := store.FindTask(slug, "proj-ok-manual")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Meta.Mode != "manual" {
		t.Errorf("mode did not round-trip, got %q", reloaded.Meta.Mode)
	}
}

// captureStdout swaps os.Stdout for a pipe, runs fn, and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return capturePipe(t, &os.Stdout, fn)
}

func TestPrintEpicPlanLabels(t *testing.T) {
	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Epic"}}
	subs := []*storage.Task{
		{Meta: storage.TaskMeta{ID: "p-1-1", Title: "ready auto", Status: storage.StatusTodo, Order: 10}},
		{Meta: storage.TaskMeta{ID: "p-1-2", Title: "manual sub", Status: storage.StatusTodo, Order: 20, Mode: "manual"}},
		{Meta: storage.TaskMeta{ID: "p-1-3", Title: "already merged", Status: storage.TaskStatus("merged"), Order: 30}},
		{Meta: storage.TaskMeta{ID: "p-1-4", Title: "parked", Status: storage.StatusWaiting, Order: 40}},
		// a manual sub already at done must report done, not MANUAL
		{Meta: storage.TaskMeta{ID: "p-1-5", Title: "manual done", Status: storage.StatusDone, Order: 50, Mode: "manual"}},
		// left on doing by a crashed run -> the plan flags it as recoverable
		{Meta: storage.TaskMeta{ID: "p-1-6", Title: "crashed", Status: storage.StatusDoing, Order: 60}},
	}
	out := captureStdout(t, func() {
		printEpicPlan(os.Stdout, tracker, "epic/p-1", "main", storage.StatusTodo, storage.TaskStatus("merged"), subs, false, "/repo", false,
			map[string]bool{"p-1-6": true})
	})
	for _, want := range []string{
		"[READY  ] p-1-1",
		"[MANUAL ] p-1-2",
		"[done   ] p-1-3",
		"[skip   ] p-1-4",
		"[done   ] p-1-5",
		"[RECOVER] p-1-6",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "integration branch: epic/p-1") {
		t.Errorf("integration plan should name the integration branch:\n%s", out)
	}

	independent := captureStdout(t, func() {
		printEpicPlan(os.Stdout, tracker, "epic/p-1", "development", storage.StatusTodo, storage.TaskStatus("merged"), subs, false, "/repo", true, nil)
	})
	if !strings.Contains(independent, "mode: INDEPENDENT") {
		t.Errorf("independent plan should announce the mode:\n%s", independent)
	}
	if !strings.Contains(independent, "off development") {
		t.Errorf("independent plan should name the fork base:\n%s", independent)
	}
	if strings.Contains(independent, "integration branch:") {
		t.Errorf("independent plan must not mention an integration branch:\n%s", independent)
	}
}

// TestPrintEpicSummaryLabel: the header used to hardcode "integration: %s", so
// an independent run - which has no integration branch at all - reported
// "(integration: independent, off main)".
func TestPrintEpicSummaryLabel(t *testing.T) {
	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Epic"}}
	outcomes := []subOutcome{{"p-1-1", "merged", "ok", "feat/one"}}

	var integration, independent strings.Builder
	printEpicSummary(&integration, tracker, "integration: epic/p-1", outcomes, "p", storage.StatusTodo, nil)
	printEpicSummary(&independent, tracker, "independent, off main", outcomes, "p", storage.StatusTodo, nil)

	if !strings.Contains(integration.String(), "(integration: epic/p-1)") {
		t.Errorf("integration summary should name the integration branch:\n%s", integration.String())
	}
	if !strings.Contains(independent.String(), "(independent, off main)") {
		t.Errorf("independent summary should name the fork base:\n%s", independent.String())
	}
	if strings.Contains(independent.String(), "integration") {
		t.Errorf("independent summary must not claim an integration branch:\n%s", independent.String())
	}
	// Both keep rendering the per-sub lines.
	for _, out := range []string{integration.String(), independent.String()} {
		if !strings.Contains(out, "merged    p-1-1  - ok") {
			t.Errorf("summary missing the sub line:\n%s", out)
		}
	}
}

// TestPrintEpicSummaryRerunSkips: pm-cli-75's silent re-run. A non-green sub
// left off the start status gets a trailing sentence naming it and how to fix
// it; an empty skip list prints nothing beyond the per-sub lines.
func TestPrintEpicSummaryRerunSkips(t *testing.T) {
	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "p-1", Title: "Epic"}}
	outcomes := []subOutcome{{"p-1-1", "failed", "claude worker failed: exit status 1", "feat/one"}}

	var withSkips strings.Builder
	printEpicSummary(&withSkips, tracker, "independent, off main", outcomes, "p", storage.StatusTodo,
		[]rerunSkip{{id: "p-1-1", status: storage.StatusDoing}})
	got := withSkips.String()
	if !strings.Contains(got, "A re-run will NOT pick up: p-1-1 (status doing)") {
		t.Errorf("summary should name the skipped sub and its status:\n%s", got)
	}
	if !strings.Contains(got, "pm mv p <id> todo") {
		t.Errorf("summary should hint the fix command:\n%s", got)
	}

	var noSkips strings.Builder
	printEpicSummary(&noSkips, tracker, "independent, off main", outcomes, "p", storage.StatusTodo, nil)
	if strings.Contains(noSkips.String(), "re-run") {
		t.Errorf("empty skip list must print nothing extra:\n%s", noSkips.String())
	}
}

// TestRerunSkips exercises the classification itself: green/skipped/manual/
// aborted subs are never listed even off the start status; a failed sub still
// sitting on doing is; a failed sub a caller already returned to todo is not.
func TestRerunSkips(t *testing.T) {
	store, slug := tempStore(t)
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1", Title: "Tracker", Status: storage.StatusTodo}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-1", Title: "stuck failed", Status: storage.StatusDoing, Parent: "proj-1"}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-2", Title: "returned to start", Status: storage.StatusTodo, Parent: "proj-1"}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-3", Title: "landed", Status: storage.StatusDone, Parent: "proj-1"}, "")
	addTask(t, store, slug, storage.TaskMeta{ID: "proj-1-4", Title: "manual", Status: storage.StatusTodo, Parent: "proj-1"}, "")

	outcomes := []subOutcome{
		{"proj-1-1", subFailed, "died", "feat/1"},
		{"proj-1-2", subFailed, "died then returned", "feat/2"},
		{"proj-1-3", subMerged, "ok", "feat/3"},
		{"proj-1-4", subManual, "manual sub", ""},
	}

	got := rerunSkips(store, slug, outcomes, storage.StatusTodo)
	if len(got) != 1 || got[0].id != "proj-1-1" || got[0].status != storage.StatusDoing {
		t.Fatalf("rerunSkips = %+v, want exactly proj-1-1 (doing)", got)
	}
}

func TestJournalSubs(t *testing.T) {
	outcomes := []subOutcome{
		{"p-1-1", "merged", "ok", "feat/one"},
		{"p-1-2", "manual", "manual sub", ""},
		{"p-1-3", "skipped", "waiting on p-1-2", ""},
	}
	durations := map[string]int{"p-1-1": 90}
	run := &storage.RunState{Subs: []storage.SubRun{
		{ID: "p-1-1", Status: "merged", Session: "sess-1", Turns: 42, CostUSD: 1.25},
		{ID: "p-1-2", Status: "manual"},
		{ID: "p-1-3", Status: "skipped"},
	}}

	got := journalSubs(outcomes, durations, run)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	first := got[0]
	if first.Session != "sess-1" || first.Turns != 42 || first.CostUSD != 1.25 || first.DurationS != 90 {
		t.Errorf("driven sub stats not attached: %+v", first)
	}
	if first.Branch != "feat/one" {
		t.Errorf("driven sub branch not attached: %+v", first)
	}
	if got[1].Branch != "" || got[2].Branch != "" {
		t.Errorf("skipped/manual subs should carry no branch: %+v %+v", got[1], got[2])
	}
	if got[1].Result != "manual" || got[1].Turns != 0 || got[1].CostUSD != 0 || got[1].DurationS != 0 {
		t.Errorf("manual sub should carry no worker stats: %+v", got[1])
	}
	if got[2].Result != "skipped" || got[2].Session != "" {
		t.Errorf("skipped sub should have no session: %+v", got[2])
	}
}

// updateSubRun stamps result+note on an existing entry after driveSub; it must
// not wipe the envelope stats driveSub stamped onto the same entry.
func TestUpdateSubRunPreservesStats(t *testing.T) {
	run := &storage.RunState{Subs: []storage.SubRun{
		{ID: "p-1-1", Status: "running", Session: "sess-1", Turns: 42, CostUSD: 1.25, Commits: []string{"abc"}},
	}}
	updateSubRun(run, "p-1-1", "merged", "all green")
	s := run.Subs[0]
	if s.Status != "merged" || s.Note != "all green" {
		t.Errorf("status/note not updated: %+v", s)
	}
	if s.Turns != 42 || s.CostUSD != 1.25 || s.Session != "sess-1" || len(s.Commits) != 1 {
		t.Errorf("updateSubRun wiped stats: %+v", s)
	}
}

func TestAnyMerged(t *testing.T) {
	if anyMerged([]subOutcome{{result: "blocked"}, {result: "skipped"}}) {
		t.Error("anyMerged = true, want false")
	}
	if !anyMerged([]subOutcome{{result: "blocked"}, {result: "merged"}}) {
		t.Error("anyMerged = false, want true")
	}
}

func TestBriefReason(t *testing.T) {
	if got := briefReason(&workerResult{Summary: "did it"}); got != "did it" {
		t.Errorf("got %q, want 'did it'", got)
	}
	if got := briefReason(&workerResult{Summary: "did it", Unresolved: []string{"x", "y"}}); got != "x; y" {
		t.Errorf("got %q, want 'x; y'", got)
	}
}

// guard: a freshly created project dir is recognised as non-git so the manager
// refuses rather than corrupting state.
func TestIsGitRepoOnPlainDir(t *testing.T) {
	dir := t.TempDir()
	if isGitRepo(dir) {
		t.Error("plain temp dir reported as git repo")
	}
	// sanity: a path that does not exist
	if isGitRepo(filepath.Join(dir, "nope")) {
		t.Error("missing dir reported as git repo")
	}
}

// TestOpenEpicPRUsesResolvedBase guards the PR-base contract: the epic PR must
// target the same resolved base the integration branch was forked from
// (--base flag > executor.base_branch > main), never a hardcoded main - with
// base_branch: development a main-targeted PR would carry every
// development-only commit in its diff.
func TestOpenEpicPRUsesResolvedBase(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	gitT(t, repo, "checkout", "-q", "-b", "development")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "base")
	gitT(t, repo, "checkout", "-q", "-b", "epic/x", "development")

	bare := t.TempDir()
	gitT(t, bare, "init", "-q", "--bare")
	gitT(t, repo, "remote", "add", "origin", bare)

	// Fake `gh` at the front of PATH records its argv instead of hitting GitHub.
	binDir := t.TempDir()
	argsFile := filepath.Join(binDir, "gh-args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tracker := &storage.Task{Meta: storage.TaskMeta{ID: "proj-1", Title: "Epic"}}
	stderr := captureStderr(t, func() {
		openEpicPR(os.Stderr, repo, "epic/x", "development", tracker)
	})

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("fake gh never ran: %v\nstderr: %s", err, stderr)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	base := ""
	head := ""
	for i, a := range args {
		if a == "--base" && i+1 < len(args) {
			base = args[i+1]
		}
		if a == "--head" && i+1 < len(args) {
			head = args[i+1]
		}
	}
	if base != "development" {
		t.Errorf("gh pr create --base = %q, want %q (argv: %v)", base, "development", args)
	}
	if head != "epic/x" {
		t.Errorf("gh pr create --head = %q, want %q", head, "epic/x")
	}
}
