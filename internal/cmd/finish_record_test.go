package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// `pm finish record` exists for one gap: a hand-driven acceptance
// (batch-finish-auto in a live session) takes the claim but writes no
// .finish.json, so once its claim lapsed the ACCEPTANCE column read "-" as if
// the run had never been accepted. The command must leave the SAME durable
// artifact a headless `pm finish` run does - verified here through
// storage.LocalRunRows, the exact aggregation `pm runs` and the board render.

func TestFinishRecordCmd(t *testing.T) {
	// acceptanceCell renders the tracker's ACCEPTANCE cell the way `pm runs`
	// would - through the one shared aggregation, never a re-count here.
	acceptanceCell := func(t *testing.T, store *storage.Store, tracker string) string {
		t.Helper()
		rows, err := storage.LocalRunRows(store, nil)
		if err != nil {
			t.Fatalf("LocalRunRows: %v", err)
		}
		for _, row := range rows {
			if row.Tracker == tracker {
				return row.Accept.String()
			}
		}
		t.Fatalf("no runs row for %s", tracker)
		return ""
	}

	t.Run("the verdict survives the claim and reaches the runs table", func(t *testing.T) {
		store, slug := finishStore(t)
		// A runs-table row exists only for a TRACKER - give proj-100 a child.
		addTask(t, store, slug, storage.TaskMeta{ID: "proj-100-1", Title: "sub", Status: storage.StatusDone, Parent: "proj-100"}, "")
		out, err := runFinishCmd(t, store, "record", "proj-100", "--project", slug,
			"--result", "partial", "--claims-open", "2", "--note", "ACME-1568 accepted by hand")
		if err != nil {
			t.Fatalf("record: %v (out: %s)", err, out)
		}
		if !strings.Contains(out, "recorded the acceptance of proj-100: partial") {
			t.Errorf("output %q does not confirm the record", out)
		}

		st, rerr := storage.ReadFinishRunState(store.ProjectDir(slug), "proj-100")
		if rerr != nil || st == nil {
			t.Fatalf("no finish run-state on disk: (%+v, %v)", st, rerr)
		}
		if st.Kind != storage.RunKindFinish || st.Status != storage.RunStatusDone {
			t.Errorf("state kind/status = %s/%s, want finish/done", st.Kind, st.Status)
		}
		if len(st.Subs) != 1 || st.Subs[0].Status != "partial" || st.Subs[0].VisualClaimsOpen != 2 {
			t.Errorf("sub = %+v, want the verdict and the open claims on subs[0]", st.Subs)
		}

		cell := acceptanceCell(t, store, "proj-100")
		if !strings.Contains(cell, "partial") || !strings.Contains(cell, "2 visual claim(s) open") {
			t.Errorf("ACCEPTANCE cell = %q, want the verdict and the open claims", cell)
		}
	})

	t.Run("only the three contracted verdicts are accepted", func(t *testing.T) {
		store, slug := finishStore(t)
		for _, bad := range []string{"", "Done", "needs a human", "verified"} {
			args := []string{"record", "proj-100", "--project", slug}
			if bad != "" {
				args = append(args, "--result", bad)
			}
			if _, err := runFinishCmd(t, store, args...); err == nil {
				t.Errorf("--result %q must be refused", bad)
			}
		}
		if _, err := storage.ReadFinishRunState(store.ProjectDir(slug), "proj-100"); err == nil {
			t.Error("a refused record must write nothing")
		}
	})

	t.Run("a live claim held by someone else refuses the write", func(t *testing.T) {
		store, slug := finishStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711, Session: "their-session",
			Started:   time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339),
			Refreshed: time.Now().UTC().Format(time.RFC3339),
		})
		_, err := runFinishCmd(t, store, "record", "proj-100", "--project", slug, "--result", "done")
		if err == nil {
			t.Fatal("recording over a stranger's live claim must fail")
		}
		for _, want := range []string{"runner", "4711"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name the holder's %q", err, want)
			}
		}
		// The matching session IS the holder: the hand acceptance recording its
		// own outcome mid-claim is the designed flow.
		out, err := runFinishCmd(t, store, "record", "proj-100", "--project", slug,
			"--result", "done", "--session", "their-session")
		if err != nil {
			t.Fatalf("the claim holder must be able to record: %v", err)
		}
		if !strings.Contains(out, "pm finish release proj-100") {
			t.Errorf("output %q should remind the holder to release the claim", out)
		}
	})

	t.Run("an expired claim does not block recording", func(t *testing.T) {
		store, slug := finishStore(t)
		seedFinishClaim(t, store, slug, storage.FinishClaim{
			TrackerID: "proj-100", Host: "runner", PID: 4711,
			Started:   time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
			Refreshed: time.Now().Add(-storage.FinishClaimTTL - time.Minute).UTC().Format(time.RFC3339),
		})
		if _, err := runFinishCmd(t, store, "record", "proj-100", "--project", slug, "--result", "done"); err != nil {
			t.Fatalf("an expired claim reads as free everywhere else, record included: %v", err)
		}
	})

	t.Run("never records over a live headless acceptance", func(t *testing.T) {
		store, slug := finishStore(t)
		addTask(t, store, slug, storage.TaskMeta{ID: "proj-100-1", Title: "sub", Status: storage.StatusDone, Parent: "proj-100"}, "")
		// This process's own pid IS a live run to ProcessAliveSinceStamp - as
		// long as the stamp is not BEFORE the process started (a pid younger
		// than the stamp reads as recycled), so the stamp is "now".
		if err := storage.WriteRunState(store.ProjectDir(slug), &storage.RunState{
			TaskID: "proj-100", Project: slug, Kind: storage.RunKindFinish,
			Status: storage.RunStatusRunning, PID: os.Getpid(),
			Started: time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			t.Fatal(err)
		}
		_, err := runFinishCmd(t, store, "record", "proj-100", "--project", slug, "--result", "done")
		if err == nil || !strings.Contains(err.Error(), "running right now") {
			t.Fatalf("recording over a live headless acceptance must be refused, got: %v", err)
		}

		// A STALE running state (dead pid) is the recovery case: record wins.
		if err := storage.WriteRunState(store.ProjectDir(slug), &storage.RunState{
			TaskID: "proj-100", Project: slug, Kind: storage.RunKindFinish,
			Status: storage.RunStatusRunning, PID: 999999,
			Started: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := runFinishCmd(t, store, "record", "proj-100", "--project", slug, "--result", "blocked"); err != nil {
			t.Fatalf("recording over a stale run-state must succeed: %v", err)
		}
		if cell := acceptanceCell(t, store, "proj-100"); !strings.Contains(cell, "blocked") {
			t.Errorf("ACCEPTANCE cell = %q, want blocked", cell)
		}
	})

	t.Run("--report saves the markdown where F opens it, and refuses an empty file", func(t *testing.T) {
		store, slug := finishStore(t)
		dir := t.TempDir()
		report := filepath.Join(dir, "report.md")
		if err := os.WriteFile(report, []byte("# Acceptance\nall good\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := runFinishCmd(t, store, "record", "proj-100", "--project", slug,
			"--result", "done", "--report", report); err != nil {
			t.Fatalf("record with report: %v", err)
		}
		saved, err := os.ReadFile(storage.FinishReportPath(store.ProjectDir(slug), "proj-100"))
		if err != nil || !strings.Contains(string(saved), "all good") {
			t.Fatalf("report not saved: (%q, %v)", saved, err)
		}

		empty := filepath.Join(dir, "empty.md")
		if err := os.WriteFile(empty, []byte("  \n"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := runFinishCmd(t, store, "record", "proj-100", "--project", slug,
			"--result", "done", "--report", empty); err == nil {
			t.Fatal("an empty --report must be refused - writeFinishReport would delete the saved one")
		}
		if _, err := os.Stat(storage.FinishReportPath(store.ProjectDir(slug), "proj-100")); err != nil {
			t.Errorf("the earlier report must survive the refused empty one: %v", err)
		}
	})
}
