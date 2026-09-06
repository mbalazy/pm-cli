package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbalazy/pm/internal/storage"
)

// floorRepo: a git repo with one commit (the diff base) and a telemetry file
// the hook can read - the two inputs the review floor is judged on.
func floorRepo(t *testing.T) (dir, base, telemetry string) {
	t.Helper()
	dir = t.TempDir()
	gitInitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x\n"), 0644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "init")
	telemetry = filepath.Join(t.TempDir(), "review.jsonl")
	storage.InitReviewTelemetry(telemetry)
	return dir, gitHeadSHA(dir), telemetry
}

func structuredOutputEvent(dir, status string) string {
	return `{"tool_name":"StructuredOutput","cwd":` + jsonString(dir) + `,"tool_input":{"status":` + jsonString(status) + `,"summary":"done","branch":"feat/x","commits":[],"unresolved":[]}}`
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The hook half of the review floor: a `verified` verdict on a real change
// with no reviewer on record is refused, everything else passes, and the
// refusal names the legitimate move.
func TestReviewFloorHook(t *testing.T) {
	run := func(t *testing.T, in string, opts guardOptions) (int, string) {
		t.Helper()
		var errOut bytes.Buffer
		code := runWorkerGuard(strings.NewReader(in), io.Discard, &errOut, opts)
		return code, errOut.String()
	}
	touch := func(t *testing.T, dir, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("changed\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("verified with a production diff and no spawn is refused", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "main.go")
		code, msg := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel, diffBase: base})
		if code != 2 {
			t.Fatalf("got exit %d, want 2 (%s)", code, msg)
		}
		for _, want := range []string{"pm review floor", "1 production file(s)", "`pm-reviewer`", "Do not return \"blocked\""} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal missing %q:\n%s", want, msg)
			}
		}
	})
	t.Run("the legacy green word is judged the same", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "main.go")
		if code, _ := run(t, structuredOutputEvent(dir, "merged"), guardOptions{telemetryPath: tel, diffBase: base}); code != 2 {
			t.Errorf("`merged` is `verified` spelled the old way, got exit %d", code)
		}
	})
	t.Run("documentation-only still needs its one reviewer", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "README.md")
		if code, msg := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel, diffBase: base}); code != 2 {
			t.Errorf("got exit %d (%s)", code, msg)
		}
	})
	t.Run("a refused spawn does not count", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "main.go")
		_ = storage.AppendReviewSpawn(tel, storage.ReviewSpawn{SubagentType: "pm-reviewer", Denied: true})
		_ = storage.AppendReviewSpawn(tel, storage.ReviewSpawn{SubagentType: "Explore", Nested: true, Denied: true})
		if code, _ := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel, diffBase: base}); code != 2 {
			t.Errorf("refused and nested spawns never ran, got exit %d", code)
		}
	})
	t.Run("a custom-type helper is not a review", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "main.go")
		_ = storage.AppendReviewSpawn(tel, storage.ReviewSpawn{SubagentType: "my-project-linter"})
		if code, _ := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel, diffBase: base}); code != 2 {
			t.Errorf("a custom-type spawn is left alone by the harness and reviews nothing, got exit %d", code)
		}
	})
	t.Run("one reviewer that ran satisfies the floor", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "main.go")
		_ = storage.AppendReviewSpawn(tel, storage.ReviewSpawn{SubagentType: "pm-reviewer", Model: "opus"})
		if code, msg := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel, diffBase: base}); code != 0 {
			t.Errorf("got exit %d (%s)", code, msg)
		}
	})
	t.Run("a typeless spawn became a reviewer and counts", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "main.go")
		_ = storage.AppendReviewSpawn(tel, storage.ReviewSpawn{})
		if code, _ := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel, diffBase: base}); code != 0 {
			t.Errorf("got exit %d", code)
		}
	})
	t.Run("a verdict other than verified passes", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "main.go")
		for _, status := range []string{"blocked", "failed"} {
			if code, _ := run(t, structuredOutputEvent(dir, status), guardOptions{telemetryPath: tel, diffBase: base}); code != 0 {
				t.Errorf("%s: got exit %d", status, code)
			}
		}
	})
	t.Run("an empty production diff passes", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		if code, _ := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel, diffBase: base}); code != 0 {
			t.Errorf("no change at all: got exit %d", code)
		}
		touch(t, dir, "main_test.go")
		if code, _ := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel, diffBase: base}); code != 0 {
			t.Errorf("a test-only change is an empty production diff: got exit %d", code)
		}
	})
	t.Run("fails open without telemetry or a diff base", func(t *testing.T) {
		dir, base, tel := floorRepo(t)
		touch(t, dir, "main.go")
		if code, _ := run(t, structuredOutputEvent(dir, "verified"), guardOptions{diffBase: base}); code != 0 {
			t.Errorf("no telemetry path: got exit %d", code)
		}
		if code, _ := run(t, structuredOutputEvent(dir, "verified"), guardOptions{telemetryPath: tel}); code != 0 {
			t.Errorf("no diff base: got exit %d", code)
		}
	})
	t.Run("the settings attach the matcher", func(t *testing.T) {
		if s := workerGuardSettings(guardOptions{telemetryPath: "/tmp/x.jsonl"}); !strings.Contains(s, `"matcher":"StructuredOutput"`) {
			t.Errorf("settings must carry the StructuredOutput matcher:\n%s", s)
		}
	})
}

// The manager half: the same rule applied to the envelope after the run. A
// fake worker that commits a change and returns verified with no reviewer on
// record ends as a parked, blocked task carrying REVIEW-SKIPPED; the same
// worker with a reviewer on record stays verified.
func TestExecuteWorkReviewFloorDemotesUnreviewedVerified(t *testing.T) {
	commitAndVerified := "echo change >> f.txt && git add f.txt && git -c user.email=t@t -c user.name=t commit -q -m worker && echo '" + envelope(workerVerified, "implemented + verified") + "'"

	t.Run("no reviewer -> blocked, parked, REVIEW-SKIPPED", func(t *testing.T) {
		fakeClaude(t, commitAndVerified)
		store, task, plan, opts := executorFixture(t)
		var errOut bytes.Buffer
		opts.errOut = &errOut

		res, err := executeWork(store, task, plan, opts)
		if err != nil {
			t.Fatalf("executeWork: %v", err)
		}
		if res.Status != workerBlocked {
			t.Fatalf("status = %s, want blocked", res.Status)
		}
		if len(res.Unresolved) == 0 || !strings.HasPrefix(res.Unresolved[len(res.Unresolved)-1], "REVIEW-SKIPPED:") {
			t.Fatalf("unresolved must end with the REVIEW-SKIPPED line: %v", res.Unresolved)
		}
		if res.Review == nil || !res.Review.Skipped || res.Review.Reviewers != 0 {
			t.Fatalf("telemetry must record the skip: %+v", res.Review)
		}
		if !strings.Contains(errOut.String(), "review floor") {
			t.Errorf("the demotion must be announced on stderr:\n%s", errOut.String())
		}
		final, _ := store.FindTask("app", "app-1")
		if final.Meta.Status != storage.StatusWaiting {
			t.Errorf("a blocked standalone result parks the task, got %s", final.Meta.Status)
		}
		if !strings.Contains(final.Meta.Brief, "REVIEW-SKIPPED") || strings.Contains(final.Meta.Brief, "ready (draft PR)") {
			t.Errorf("brief must carry the demotion, not a green word: %q", final.Meta.Brief)
		}
		run, _ := storage.ReadRunState(store.ProjectDir("app"), "app-1")
		if run == nil || len(run.Subs) == 0 || run.Subs[0].Status != workerBlocked || run.Subs[0].Review == nil || !run.Subs[0].Review.Skipped {
			t.Errorf("run-state must show the demoted sub: %+v", run)
		}
		entries, _ := storage.ReadJournal(store.ProjectDir("app"))
		if len(entries) != 2 || len(entries[1].Subs) != 1 || entries[1].Subs[0].Result != workerBlocked || !entries[1].Subs[0].Review.Skipped {
			t.Errorf("journal must carry result blocked + skipped: %+v", entries)
		}
	})
	t.Run("a reviewer on record keeps verified", func(t *testing.T) {
		store, task, plan, opts := executorFixture(t)
		tel := storage.ReviewTelemetryPath(store.ProjectDir("app"), plan.sessionID)
		// The fake worker "spawns" its reviewer by writing the line the hook
		// would have written, then commits and returns verified.
		fakeClaude(t, `printf '{"ts":"2026-09-06T10:00:00Z","subagent_type":"pm-reviewer","model":"opus"}\n' >> '`+tel+`' && `+commitAndVerified)
		res, err := executeWork(store, task, plan, opts)
		if err != nil {
			t.Fatalf("executeWork: %v", err)
		}
		if res.Status != workerVerified || res.Review == nil || res.Review.Skipped || res.Review.Reviewers != 1 {
			t.Fatalf("one reviewer that ran satisfies the floor: status=%s review=%+v", res.Status, res.Review)
		}
	})
	t.Run("no change at all keeps verified", func(t *testing.T) {
		fakeClaude(t, "echo '"+envelope(workerVerified, "nothing to change")+"'")
		store, task, plan, opts := executorFixture(t)
		res, err := executeWork(store, task, plan, opts)
		if err != nil {
			t.Fatalf("executeWork: %v", err)
		}
		if res.Status != workerVerified || (res.Review != nil && res.Review.Skipped) {
			t.Fatalf("an empty production diff has nothing to review: status=%s review=%+v", res.Status, res.Review)
		}
	})
}

// A demoted sub shows up in the stats rollup as its own line.
func TestRenderJournalStatsReviewSkipped(t *testing.T) {
	st := aggregateJournal([]storage.JournalEntry{
		startEntry("run-epic", "app-1", 100),
		{
			Event: storage.JournalEventEnd, Kind: "run-epic", TaskID: "app-1", PID: 100,
			Status: "done", DurationS: 600,
			Subs: []storage.JournalSub{
				{ID: "app-1-1", Result: "merged", Turns: 20, Review: &storage.ReviewTelemetry{Spawns: 1, Reviewers: 1, Rounds: 1}},
				{ID: "app-1-2", Result: "blocked", Turns: 10, Review: &storage.ReviewTelemetry{Spawns: 0, Skipped: true}},
			},
		},
	}, ignoreRef(noneAlive), testNow)
	if st.ReviewSkipped != 1 {
		t.Fatalf("ReviewSkipped = %d, want 1", st.ReviewSkipped)
	}
	out := renderJournalStats("app", "/tmp/j.jsonl", st)
	if !strings.Contains(out, "review skipped: 1 sub(s) returned verified with 0 reviewer spawns") {
		t.Errorf("stats missing the floor line:\n%s", out)
	}
}
