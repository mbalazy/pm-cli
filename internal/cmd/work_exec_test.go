package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// fakeClaude installs a fake `claude` binary (a shell script emitting `script`)
// at the front of PATH, so executeWork/runWorker exercise their real plumbing
// without spending tokens.
func fakeClaude(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func envelope(status, summary string) string {
	return fmt.Sprintf(`{"type":"result","subtype":"success","is_error":false,"result":"","session_id":"sess-fake","num_turns":3,"total_cost_usd":0.25,"structured_output":{"status":%q,"summary":%q,"branch":"feat/x","commits":["abc1234"],"unresolved":["TODO: sim check"]}}`, status, summary)
}

// executorFixture: a store with one project backed by a real git repo, one
// doing-task, and a ready workPlan.
func executorFixture(t *testing.T) (*storage.Store, *storage.Task, *workPlan, workOptions) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	repo := t.TempDir()
	gitInitRepo(t, repo)
	os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644)
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	if err := store.CreateProject("app", &storage.Project{Name: "App", Path: repo, Executor: &storage.Executor{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	task := &storage.Task{Meta: storage.TaskMeta{ID: "app-1", Title: "Fix thing", Status: storage.StatusDoing}, Project: "app"}
	if err := store.AddTask("app", task); err != nil {
		t.Fatal(err)
	}
	task, err := store.FindTask("app", "app-1")
	if err != nil {
		t.Fatal(err)
	}

	opts := workOptions{standalone: true, model: "opus", maxTurns: 10, timeout: 30 * 1e9}
	plan, err := planWork(store, task, "app", opts)
	if err != nil {
		t.Fatalf("planWork: %v", err)
	}
	return store, task, plan, opts
}

func TestExecuteWorkMergedRecordsEverything(t *testing.T) {
	fakeClaude(t, "echo '"+envelope("merged", "implemented + verified")+"'")
	store, task, plan, opts := executorFixture(t)

	res, err := executeWork(store, task, plan, opts)
	if err != nil {
		t.Fatalf("executeWork: %v", err)
	}
	if res.Status != "merged" || res.Turns != 3 || res.CostUSD != 0.25 {
		t.Fatalf("result not lifted from envelope: %+v", res)
	}

	final, _ := store.FindTask("app", "app-1")
	if !strings.Contains(final.Meta.Brief, "ready (draft PR)") {
		t.Fatalf("brief not written: %q", final.Meta.Brief)
	}
	if len(final.Meta.Sessions) == 0 || final.Meta.Sessions[len(final.Meta.Sessions)-1] != "sess-fake" {
		t.Fatalf("worker session not attached: %v", final.Meta.Sessions)
	}
	if final.Meta.Status != storage.StatusDoing {
		t.Fatalf("merged must NEVER auto-move the task (autonomy envelope), got %s", final.Meta.Status)
	}

	run, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil || run.Status != storage.RunStatusDone {
		t.Fatalf("run-state: %+v err=%v", run, err)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || entries[0].Event != "start" || entries[1].Event != "end" || entries[1].Status != storage.RunStatusDone {
		t.Fatalf("journal: %+v", entries)
	}
	if entries[0].Baseline != "" || entries[1].Baseline != "" {
		t.Fatalf("no executor.baseline configured -> journal Baseline must stay empty: %+v", entries)
	}
}

func TestExecuteWorkBlockedParksOnWaiting(t *testing.T) {
	fakeClaude(t, "echo '"+envelope("blocked", "need product decision")+"'")
	store, task, plan, opts := executorFixture(t)

	res, err := executeWork(store, task, plan, opts)
	if err != nil {
		t.Fatalf("executeWork: %v", err)
	}
	if res.Status != "blocked" {
		t.Fatalf("status = %s", res.Status)
	}
	final, _ := store.FindTask("app", "app-1")
	if final.Meta.Status != storage.StatusWaiting {
		t.Fatalf("blocked task must park on waiting, got %s", final.Meta.Status)
	}
}

func TestExecuteWorkBlockedIndependentStaysPut(t *testing.T) {
	fakeClaude(t, "echo '"+envelope("blocked", "handoff recorded")+"'")
	store, task, plan, opts := executorFixture(t)
	opts.independent = true

	if _, err := executeWork(store, task, plan, opts); err != nil {
		t.Fatalf("executeWork: %v", err)
	}
	final, _ := store.FindTask("app", "app-1")
	if final.Meta.Status != storage.StatusDoing {
		t.Fatalf("independent mode must not park, got %s", final.Meta.Status)
	}
}

func TestExecuteWorkWorkerCrashRecordsFailure(t *testing.T) {
	fakeClaude(t, "exit 1")
	store, task, plan, opts := executorFixture(t)

	if _, err := executeWork(store, task, plan, opts); err == nil {
		t.Fatal("crashing worker must surface an error")
	}
	run, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil || run.Status != storage.RunStatusFailed || run.Error == "" {
		t.Fatalf("run-state must record the failure: %+v err=%v", run, err)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || entries[1].Status != storage.RunStatusFailed {
		t.Fatalf("journal must record failed end: %+v", entries)
	}
}

// TestExecuteWorkApplyResultErrorStillJournalsEnd covers Bug 1 (pm-cli-37):
// when the worker itself succeeds but applyWorkerResult fails (a storage-layer
// error, unrelated to the work outcome), executeWork must still append a
// journal "end" line before returning - otherwise the "start" line is left
// without a partner and pm executor stats misreads it as a crashed run.
// applyWorkerResult is forced to fail by corrupting the task file on disk
// (directly, bypassing store validation) with an invalid `mode` value: the
// fresh re-read inside applyWorkerResult picks it up, and the final
// store.WriteTask rejects it via storage.ValidateMode.
func TestExecuteWorkApplyResultErrorStillJournalsEnd(t *testing.T) {
	fakeClaude(t, "echo '"+envelope("merged", "implemented + verified")+"'")
	store, task, plan, opts := executorFixture(t)

	raw, err := os.ReadFile(task.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := strings.Replace(string(raw), "---\n", "---\nmode: bogus\n", 1)
	if err := os.WriteFile(task.FilePath, []byte(corrupted), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := executeWork(store, task, plan, opts); err == nil {
		t.Fatal("applyWorkerResult failure must surface as an executeWork error")
	} else if !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("expected the invalid-mode error to propagate, got: %v", err)
	}

	run, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil || run.Status != storage.RunStatusFailed || run.Error == "" {
		t.Fatalf("run-state must record the failure: %+v err=%v", run, err)
	}
	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || entries[0].Event != "start" || entries[1].Event != "end" {
		t.Fatalf("expected start+end journal pair (no phantom crash), got: %+v", entries)
	}
	if entries[1].Status != storage.RunStatusFailed {
		t.Fatalf("end line must record the failure, got: %+v", entries[1])
	}
	if len(entries[1].Subs) != 1 || entries[1].Subs[0].Result != "failed" {
		t.Fatalf("end line must carry a failed sub outcome, got: %+v", entries[1].Subs)
	}
	// The worker itself succeeded (envelope carries num_turns=3, total_cost_usd=0.25) -
	// that real spend must not be silently dropped just because the later
	// storage write failed.
	if entries[1].Subs[0].Turns != 3 || entries[1].Subs[0].CostUSD != 0.25 {
		t.Errorf("end line must carry the worker's real turns/cost, got: %+v", entries[1].Subs[0])
	}
	if run.Subs[0].Turns != 3 || run.Subs[0].CostUSD != 0.25 {
		t.Errorf("run-state sub must carry the worker's real turns/cost, got: %+v", run.Subs[0])
	}
}

func TestExecuteWorkGarbageOutputIsError(t *testing.T) {
	fakeClaude(t, "echo 'not json at all'")
	store, task, plan, opts := executorFixture(t)

	if _, err := executeWork(store, task, plan, opts); err == nil {
		t.Fatal("unparseable worker output must surface an error")
	}
}

func TestExecuteWorkNoStructuredOutputIsError(t *testing.T) {
	fakeClaude(t, `echo '{"type":"result","is_error":true,"result":"ran out of turns","session_id":"s"}'`)
	store, task, plan, opts := executorFixture(t)

	_, err := executeWork(store, task, plan, opts)
	if err == nil || !strings.Contains(err.Error(), "no structured result") {
		t.Fatalf("expected 'no structured result' error, got %v", err)
	}
}
