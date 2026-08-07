package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// Tests for the MAIN form of `pm finish` - the acceptance run. Everything here
// goes through the FAKE `claude` binary (fakeClaude, see work_exec_test.go), so
// the real plumbing runs without a single token being spent.

// finishRunFixture: a store with one project backed by a real git repo, one
// tracker, and a ready finishPlan. The returned buffer is the run's stderr.
func finishRunFixture(t *testing.T) (*storage.Store, *finishPlan, *bytes.Buffer) {
	t.Helper()
	store := &storage.Store{Root: t.TempDir()}
	repo := t.TempDir()
	gitInitRepo(t, repo)
	if err := os.WriteFile(repo+"/f.txt", []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", "init")

	if err := store.CreateProject("app", &storage.Project{Name: "App", Path: repo, Executor: &storage.Executor{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	addTask(t, store, "app", storage.TaskMeta{ID: "app-9", Title: "Batch tracker", Status: storage.StatusDoing}, "")
	tracker, err := store.FindTask("app", "app-9")
	if err != nil {
		t.Fatal(err)
	}

	log := &bytes.Buffer{}
	opts := finishOptions{model: "opus", maxTurns: 10, yolo: true, timeout: 30 * time.Second, errOut: log}
	plan, err := planFinish(store, tracker, "app", opts)
	if err != nil {
		t.Fatalf("planFinish: %v", err)
	}
	return store, plan, log
}

// fakeClaudeEnvelope installs a fake `claude` that emits payload verbatim. It
// goes through a file rather than `echo`, because /bin/sh's echo expands
// backslash escapes and would turn a JSON-escaped newline inside the report
// into a real one - i.e. into invalid JSON.
func fakeClaudeEnvelope(t *testing.T, payload string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "envelope.json")
	if err := os.WriteFile(path, []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}
	fakeClaude(t, "cat "+path)
}

// finishEnvelopeJSON renders a `claude -p --output-format json` envelope
// carrying an acceptance result.
func finishEnvelopeJSON(t *testing.T, status, summary, report string, subs ...finishSub) string {
	t.Helper()
	if subs == nil {
		subs = []finishSub{}
	}
	b, err := json.Marshal(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "result": "",
		"session_id": "sess-finish", "num_turns": 7, "total_cost_usd": 1.5,
		"structured_output": map[string]any{
			"status": status, "summary": summary, "report": report, "subs": subs,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExecuteFinishGreenPathRecordsEverything(t *testing.T) {
	store, plan, log := finishRunFixture(t)
	dir := store.ProjectDir("app")
	// The run being ACCEPTED already has a run-state under the same task id.
	// It must come out of this untouched - that split is the whole of
	// pm-cli-100-2, and an acceptance that overwrote it would destroy the
	// record of the run it is accepting.
	runOfTheRun := &storage.RunState{TaskID: "app-9", Project: "app", Kind: storage.RunKindEpic, Status: storage.RunStatusDone, PID: 4711}
	if err := storage.WriteRunState(dir, runOfTheRun); err != nil {
		t.Fatal(err)
	}
	fakeClaudeEnvelope(t, finishEnvelopeJSON(t, finishDone, "accepted 2 subs", "# Report\n\n| sub | verdict |\n",
		finishSub{ID: "app-9-1", Verdict: "clean", VisualClaimsOpen: 2, PushedCommits: []string{}},
		finishSub{ID: "app-9-2", Verdict: "fixed", VisualClaimsOpen: 0, PushedCommits: []string{"abc1234"}}))

	res, err := executeFinish(plan)
	if err != nil {
		t.Fatalf("executeFinish: %v", err)
	}
	if res.Status != finishDone || len(res.Subs) != 2 {
		t.Fatalf("result not parsed: %+v", res)
	}
	if res.Subs[0].VisualClaimsOpen != 2 || res.Turns != 7 || res.CostUSD != 1.5 {
		t.Fatalf("visual claims / run stats not lifted: %+v", res)
	}

	run, err := storage.ReadFinishRunState(dir, "app-9")
	if err != nil {
		t.Fatalf("acceptance run-state: %v", err)
	}
	if run.Kind != storage.RunKindFinish || run.Status != storage.RunStatusDone {
		t.Fatalf("acceptance run-state = %+v", run)
	}
	if run.CurrentSession != "" || run.WorkerPGID != 0 {
		t.Errorf("the in-flight markers must be cleared when the worker returns: session=%q pgid=%d", run.CurrentSession, run.WorkerPGID)
	}
	if len(run.Subs) != 1 || !strings.Contains(run.Subs[0].Note, "2 visual claim(s) still open") {
		t.Errorf("the open visual claims must reach the run-state note, got %+v", run.Subs)
	}

	kept, err := storage.ReadRunState(dir, "app-9")
	if err != nil || kept.Kind != storage.RunKindEpic || kept.PID != 4711 {
		t.Fatalf("the accepted run's own run-state was clobbered: %+v err=%v", kept, err)
	}

	report, err := os.ReadFile(storage.FinishReportPath(dir, "app-9"))
	if err != nil || !strings.Contains(string(report), "| sub | verdict |") {
		t.Fatalf("report not written: %q err=%v", report, err)
	}

	entries, _ := storage.ReadJournal(dir)
	if len(entries) != 2 || entries[0].Event != "start" || entries[1].Event != "end" {
		t.Fatalf("journal must hold a start+end pair: %+v", entries)
	}
	if entries[0].Kind != storage.RunKindFinish || entries[1].Kind != storage.RunKindFinish {
		t.Errorf("journal lines must be booked as their own run kind: %+v", entries)
	}
	if entries[1].Status != storage.RunStatusDone || entries[1].RunID == "" || entries[0].RunID != entries[1].RunID {
		t.Errorf("end line / run id: %+v", entries)
	}
	if len(entries[1].Subs) != 1 || entries[1].Subs[0].Result != finishDone || entries[1].Subs[0].Turns != 7 {
		t.Errorf("end line must carry the acceptance outcome: %+v", entries[1].Subs)
	}

	if claim, cerr := storage.ReadFinishClaim(dir, "app-9"); cerr != nil || claim != nil {
		t.Errorf("the claim must be released when the run ends: (%+v, %v)", claim, cerr)
	}
	if !strings.Contains(log.String(), "report written to") {
		t.Errorf("stderr should say where the report went: %q", log.String())
	}
}

// The envelope carries structured_output only while StructuredOutput is the
// run's LAST act. The rescue reads it back out of the transcript, and says so.
func TestExecuteFinishRecoversResultFromTranscript(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	store, plan, log := finishRunFixture(t)
	writeTranscript(t, cfg, plan.workDir, "sess-finish", transcriptLine(t, map[string]any{
		"status":  finishPartial,
		"summary": "one sub still needs a human",
		"report":  "# Report",
		"subs": []any{map[string]any{
			"id": "app-9-1", "verdict": "blocked", "visual_claims_open": 3, "pushed_commits": []any{},
		}},
	}))
	fakeClaude(t, `echo '{"type":"result","is_error":false,"result":"one more thing","session_id":"sess-finish","num_turns":9,"total_cost_usd":2.5}'`)

	res, err := executeFinish(plan)
	if err != nil {
		t.Fatalf("expected recovery, got %v", err)
	}
	if res.Status != finishPartial || len(res.Subs) != 1 || res.Subs[0].VisualClaimsOpen != 3 {
		t.Fatalf("recovered result = %+v", res)
	}
	// Turns and cost exist only in the envelope - a recovery that dropped them
	// would zero out every retro's effort/cost numbers.
	if res.Turns != 9 || res.CostUSD != 2.5 {
		t.Errorf("run stats not lifted from the envelope: turns=%d cost=%v", res.Turns, res.CostUSD)
	}
	if !strings.Contains(log.String(), "recovered status") {
		t.Errorf("a silent recovery hides the worker's turn order behind pm: %q", log.String())
	}
	run, err := storage.ReadFinishRunState(store.ProjectDir("app"), "app-9")
	if err != nil || run.Status != storage.RunStatusDone {
		t.Fatalf("a recovered run is a finished run: %+v err=%v", run, err)
	}
}

// A worker that dies without an envelope must say what it said - the four
// spend-limit deaths that motivated workerLastWords reported six words and no
// cause.
func TestExecuteFinishWorkerDeathCarriesLastWords(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	store, plan, _ := finishRunFixture(t)
	writeTranscript(t, cfg, plan.workDir, plan.sessionID, `{"type":"assistant","message":{"content":[{"type":"text","text":"the acceptance rig is unreachable"}]}}`)
	fakeClaude(t, "exit 1")

	_, err := executeFinish(plan)
	if err == nil {
		t.Fatal("a crashing acceptance worker must surface an error")
	}
	if !strings.Contains(err.Error(), "the acceptance rig is unreachable") {
		t.Errorf("error must carry the worker's last words, got: %v", err)
	}

	dir := store.ProjectDir("app")
	run, rerr := storage.ReadFinishRunState(dir, "app-9")
	if rerr != nil || run.Status != storage.RunStatusFailed || run.Error == "" {
		t.Fatalf("run-state must record the failure: %+v err=%v", run, rerr)
	}
	if claim, cerr := storage.ReadFinishClaim(dir, "app-9"); cerr != nil || claim != nil {
		t.Errorf("a failed run must still release its claim: (%+v, %v)", claim, cerr)
	}
	entries, _ := storage.ReadJournal(dir)
	if len(entries) != 2 || entries[1].Status != storage.RunStatusFailed {
		t.Fatalf("journal must record a failed end: %+v", entries)
	}
}

// A live claim held by somebody else costs the error message and nothing else:
// no worker, no run-state, no journal line.
func TestExecuteFinishRefusesWhenTheClaimIsHeld(t *testing.T) {
	store, plan, _ := finishRunFixture(t)
	dir := store.ProjectDir("app")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	seedFinishClaim(t, store, "app", storage.FinishClaim{
		TrackerID: "app-9", Host: "other-host", PID: 321, Started: now, Refreshed: now,
	})
	fakeClaude(t, "echo 'the worker must never run'; exit 0")

	_, err := executeFinish(plan)
	if err == nil {
		t.Fatal("a held claim must stop the run")
	}
	if !strings.Contains(err.Error(), "other-host") {
		t.Errorf("the refusal must name the holder, got: %v", err)
	}
	if _, serr := os.Stat(storage.FinishRunPath(dir, "app-9")); !os.IsNotExist(serr) {
		t.Error("a refused acceptance still wrote a run-state")
	}
	if entries, _ := storage.ReadJournal(dir); len(entries) != 0 {
		t.Errorf("a refused acceptance still journalled: %+v", entries)
	}
	// The other session's claim must be exactly where it was.
	claim, cerr := storage.ReadFinishClaim(dir, "app-9")
	if cerr != nil || claim == nil || claim.Host != "other-host" {
		t.Fatalf("the holder's claim was disturbed: (%+v, %v)", claim, cerr)
	}
}

// A dry-run prints the plan and takes nothing away from a real one.
func TestFinishDryRunClaimsNothing(t *testing.T) {
	store, _, _ := finishRunFixture(t)
	dir := store.ProjectDir("app")

	out, err := runFinishCmd(t, store, "app-9", "--project", "app", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v (out: %s)", err, out)
	}
	for _, want := range []string{"pm finish (dry-run)", "app-9", "claim: free (no claim)", "SYSTEM PROMPT", "batch-finish-auto"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}
	if _, serr := os.Stat(storage.FinishClaimPath(dir, "app-9")); !os.IsNotExist(serr) {
		t.Error("--dry-run claimed the run")
	}
	if _, serr := os.Stat(storage.FinishRunPath(dir, "app-9")); !os.IsNotExist(serr) {
		t.Error("--dry-run wrote a run-state")
	}
	if entries, _ := storage.ReadJournal(dir); len(entries) != 0 {
		t.Errorf("--dry-run journalled: %+v", entries)
	}
}

func TestFinishClaudeArgs(t *testing.T) {
	base := finishOptions{model: "opus", maxTurns: 10, yolo: true}

	t.Run("yolo by default, guard attached, no reviewer agent", func(t *testing.T) {
		args := buildClaudeArgsFor(finishClaudeRun("p", "sp", "sess", base))
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--dangerously-skip-permissions") {
			t.Error("the acceptance run is yolo by default: the odbiór procedure is broad and a curated allowlist blocks it at 3am")
		}
		if !strings.Contains(joined, "--settings") || !strings.Contains(joined, "worker-guard") {
			t.Error("the guard hook rides along in yolo too - it is what protects .git and the hooks")
		}
		if strings.Contains(joined, "--agents") {
			t.Error("the acceptance has no review phase, so no reviewer agent type must be defined for it")
		}
		if !strings.Contains(joined, "visual_claims_open") {
			t.Error("the acceptance must be validated against its OWN schema, not the worker's")
		}
		if strings.Contains(joined, "--telemetry") || strings.Contains(joined, "--diff-base") || strings.Contains(joined, "--fix-rounds") {
			t.Error("a review cap sized from a diff would kill the acceptance on its second sub - the guard must carry none of it")
		}
	})

	t.Run("--no-yolo falls back to the curated lists", func(t *testing.T) {
		opts := base
		opts.yolo = false
		joined := strings.Join(buildClaudeArgsFor(finishClaudeRun("p", "sp", "sess", opts)), " ")
		if strings.Contains(joined, "--dangerously-skip-permissions") {
			t.Error("--no-yolo must actually switch it off")
		}
		if !strings.Contains(joined, "--allowedTools") || !strings.Contains(joined, "--disallowedTools") {
			t.Error("without yolo the curated allow/disallow lists must be passed")
		}
	})
}

// The spawn cap that guards review economics must not reach the acceptance: it
// spawns one agent per SUB of a batch, a count that has nothing to do with a
// diff. Nested spawns stay blocked, deliberately.
func TestFinishGuardAllowsPerSubSpawnsButNotNestedOnes(t *testing.T) {
	if reason := judgeAgentSpawn(guardOptions{}, hookEvent{ToolName: "Agent"}, time.Now()); reason != "" {
		t.Errorf("an acceptance subagent spawn must be allowed, got: %s", reason)
	}
	nested := hookEvent{ToolName: "Agent", AgentID: "a1", AgentType: "general-purpose"}
	if reason := judgeAgentSpawn(guardOptions{}, nested, time.Now()); reason == "" {
		t.Error("a nested spawn must stay blocked even with no telemetry configured")
	}
}

func TestFinishReportIsNotWrittenWhenEmpty(t *testing.T) {
	store, plan, log := finishRunFixture(t)
	fakeClaudeEnvelope(t, finishEnvelopeJSON(t, finishBlocked, "could not find the run", ""))

	if _, err := executeFinish(plan); err != nil {
		t.Fatalf("executeFinish: %v", err)
	}
	if _, serr := os.Stat(storage.FinishReportPath(store.ProjectDir("app"), "app-9")); !os.IsNotExist(serr) {
		t.Error("an empty report must write no file - an empty one reads as 'nothing to say', which is a different claim")
	}
	if !strings.Contains(log.String(), "returned no report") {
		t.Errorf("a missing report must be announced: %q", log.String())
	}
}

func TestStartFinishClaimRefreshKeepsItAlive(t *testing.T) {
	store, slug := finishStore(t)
	dir := store.ProjectDir(slug)
	claim, err := storage.AcquireFinishClaim(dir, "proj-100", "sess")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	first := claim.Refreshed

	var log bytes.Buffer
	stop := startFinishClaimRefresh(&log, dir, "proj-100", claim, 5*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		on, rerr := storage.ReadFinishClaim(dir, "proj-100")
		if rerr == nil && on != nil && on.Refreshed != first {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	stop() // idempotent, and it waits for the goroutine - so nothing can touch the claim after this

	on, rerr := storage.ReadFinishClaim(dir, "proj-100")
	if rerr != nil || on == nil {
		t.Fatalf("claim gone: (%+v, %v)", on, rerr)
	}
	if on.Refreshed == first {
		t.Fatalf("the refresher never moved the stamp (still %s)", first)
	}
	if on.Started != claim.Started || on.Session != "sess" {
		t.Errorf("a refresh must not rewrite the claim's identity or payload: %+v", on)
	}
	if err := storage.ReleaseFinishClaim(dir, "proj-100", claim); err != nil {
		t.Errorf("the refreshed claim must still be releasable by its holder: %v", err)
	}
}

// `pm executor stats` must simply count an acceptance as its own kind of run
// rather than tripping over an unfamiliar one.
func TestAggregateJournalCountsFinishRuns(t *testing.T) {
	entries := []storage.JournalEntry{
		{Event: storage.JournalEventStart, TS: "2026-08-07T10:00:00Z", Kind: storage.RunKindFinish, TaskID: "app-9", RunID: "r1", PID: 10},
		{Event: storage.JournalEventEnd, TS: "2026-08-07T10:40:00Z", Kind: storage.RunKindFinish, TaskID: "app-9", RunID: "r1", PID: 10,
			Status: storage.RunStatusDone, DurationS: 2400,
			Subs: []storage.JournalSub{{ID: "app-9", Result: finishDone, Turns: 7, CostUSD: 1.5}}},
	}
	st := aggregateJournal(entries, func(int, time.Time) bool { return false }, time.Now())
	if st.Runs != 1 || st.Crashes != 0 {
		t.Fatalf("runs=%d crashes=%d, want 1/0", st.Runs, st.Crashes)
	}
	k := st.Kinds[storage.RunKindFinish]
	if k == nil || k.Runs != 1 || k.Statuses[storage.RunStatusDone] != 1 {
		t.Fatalf("finish runs must be booked under their own kind: %+v", st.Kinds)
	}
	if st.SubResults[finishDone] != 1 || st.Turns.Total != 7 {
		t.Errorf("acceptance outcomes must be counted: %+v turns=%v", st.SubResults, st.Turns)
	}
	out := renderJournalStats("app", storage.JournalPath("/tmp"), st)
	if !strings.Contains(out, storage.RunKindFinish) {
		t.Errorf("stats output does not mention the acceptance kind:\n%s", out)
	}
}

func TestFinishPromptPointsAtTheSkillAndTheSimMode(t *testing.T) {
	store, plan, _ := finishRunFixture(t)

	// The prompt must POINT at the skill, never restate it: the odbiór
	// procedure is ~630 lines across two SKILL.md files, and a copy here is a
	// second copy to drift.
	if !strings.Contains(plan.prompt, "batch-finish-auto") || !strings.Contains(plan.prompt, "--no-sim") {
		t.Errorf("prompt must name the skill and its sim flag:\n%s", plan.prompt)
	}
	if !strings.Contains(plan.prompt, plan.reportPath) || !strings.Contains(plan.prompt, plan.workDir) {
		t.Errorf("prompt must state where the report goes and where the run stands:\n%s", plan.prompt)
	}
	if !strings.Contains(plan.prompt, "FINAL act") {
		t.Error("the prompt must demand the result as the last act - a late background notification empties the envelope")
	}
	if len(plan.prompt) > 3000 {
		t.Errorf("the acceptance prompt is meant to be SHORT (%d chars) - the procedure belongs in the skill", len(plan.prompt))
	}

	tracker, err := store.FindTask("app", "app-9")
	if err != nil {
		t.Fatal(err)
	}
	withSim, err := planFinish(store, tracker, "app", finishOptions{model: "opus", maxTurns: 10, sim: true})
	if err != nil {
		t.Fatalf("planFinish: %v", err)
	}
	if !strings.Contains(withSim.prompt, "--sim") || strings.Contains(withSim.prompt, "--no-sim") {
		t.Errorf("--sim must reach the skill as its own flag:\n%s", withSim.prompt)
	}
	if !strings.Contains(buildFinishSystemPrompt(false), "stays OPEN") {
		t.Error("a detached acceptance must be told its visual claims stay open, not guessed")
	}
}

func TestFinishRefusesContradictorySimFlags(t *testing.T) {
	store, _, _ := finishRunFixture(t)
	out, err := runFinishCmd(t, store, "app-9", "--project", "app", "--sim", "--no-sim", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "contradict") {
		t.Fatalf("--sim --no-sim must be refused, got err=%v out=%s", err, out)
	}
}

// --additional is refused rather than quietly running in the main checkout:
// slot handling for the acceptance run is pm-cli-100-5's, and a silent
// fallback would put an acceptance into the very checkout a slot exists to
// keep it out of.
func TestFinishAdditionalIsRefusedNotIgnored(t *testing.T) {
	store, _, _ := finishRunFixture(t)
	tracker, err := store.FindTask("app", "app-9")
	if err != nil {
		t.Fatal(err)
	}
	_, err = planFinish(store, tracker, "app", finishOptions{model: "opus", maxTurns: 10, additional: true})
	if err == nil || !strings.Contains(err.Error(), "pm-cli-100-5") {
		t.Fatalf("--additional must be refused with a pointer to where it lands, got: %v", err)
	}
}
