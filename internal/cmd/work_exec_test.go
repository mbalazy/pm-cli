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
	gitT(t, repo, "init", "-q")
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
