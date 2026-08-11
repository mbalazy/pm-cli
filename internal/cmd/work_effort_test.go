package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// assistantUsage builds one assistant record carrying token usage, the way a
// real transcript does.
func assistantUsage(outputTokens int, sidechain bool) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%v,"message":{"role":"assistant","content":[{"type":"text","text":"working"}],"usage":{"input_tokens":10,"output_tokens":%d}}}`,
		sidechain, outputTokens)
}

func userTurn(sidechain bool) string {
	return fmt.Sprintf(`{"type":"user","isSidechain":%v,"message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`, sidechain)
}

// TestScanWorkerEffort is the measurement itself: a worker that died after real
// work must come back with real numbers, and every "nothing to report" case must
// come back nil rather than as a zero-filled struct (which would claim pm
// measured a worker that did nothing).
func TestScanWorkerEffort(t *testing.T) {
	t.Run("counts main-loop turns and output tokens", func(t *testing.T) {
		tr := strings.Join([]string{
			`{"type":"user","message":{"role":"user","content":"# Task: do the thing"}}`,
			assistantUsage(120, false),
			userTurn(false),
			assistantUsage(80, false),
			`{"type":"last-prompt"}`,
		}, "\n")
		got := scanWorkerEffort(strings.NewReader(tr))
		if got == nil {
			t.Fatal("got nil, want the reconstructed effort")
		}
		if got.Source != effortSourceTranscript {
			t.Errorf("Source = %q, want %q", got.Source, effortSourceTranscript)
		}
		if got.Turns != 2 || got.OutputTokens != 200 {
			t.Errorf("Turns/OutputTokens = %d/%d, want 2/200", got.Turns, got.OutputTokens)
		}
	})

	t.Run("a subagent's sidechain is not the worker's own effort", func(t *testing.T) {
		tr := strings.Join([]string{
			`{"type":"user","message":{"role":"user","content":"# Task"}}`,
			assistantUsage(50, false),
			userTurn(true),
			assistantUsage(9999, true),
		}, "\n")
		got := scanWorkerEffort(strings.NewReader(tr))
		if got == nil {
			t.Fatal("got nil, want the main loop's effort")
		}
		if got.Turns != 1 || got.OutputTokens != 50 {
			t.Errorf("Turns/OutputTokens = %d/%d, want 1/50 - sidechain records must not count", got.Turns, got.OutputTokens)
		}
	})

	t.Run("a truncated final line does not lose the rest", func(t *testing.T) {
		tr := assistantUsage(70, false) + "\n" + `{"type":"assist`
		got := scanWorkerEffort(strings.NewReader(tr))
		if got == nil || got.OutputTokens != 70 {
			t.Fatalf("got %+v, want the parsable records to still count", got)
		}
	})

	t.Run("no assistant record means nothing to report", func(t *testing.T) {
		for name, tr := range map[string]string{
			"empty transcript":   "",
			"only user records":  userTurn(false) + "\n" + userTurn(false),
			"only sidechain":     assistantUsage(500, true),
			"unparsable content": "not json at all\n{\n",
		} {
			if got := scanWorkerEffort(strings.NewReader(tr)); got != nil {
				t.Errorf("%s: got %+v, want nil", name, got)
			}
		}
	})
}

// TestRecoverWorkerEffortFindsTheTranscript: the recovery has to work off the
// session id pm MINTED (the dead worker never reported one), through the same
// candidate paths as the last-words recovery.
func TestRecoverWorkerEffortFindsTheTranscript(t *testing.T) {
	cfg := t.TempDir()
	dir := t.TempDir()
	sessionID := "11111111-2222-3333-4444-555555555555"
	path := filepath.Join(cfg, "projects", encodeProjectPath(dir), sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	tr := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"# Task"}}`,
		assistantUsage(1000, false),
		userTurn(false),
		assistantUsage(2000, false),
	}, "\n")
	if err := os.WriteFile(path, []byte(tr), 0644); err != nil {
		t.Fatal(err)
	}

	got := recoverWorkerEffort(cfg, dir, sessionID)
	if got == nil || got.Turns != 2 || got.OutputTokens != 3000 {
		t.Fatalf("got %+v, want 2 turns / 3000 output tokens from %s", got, path)
	}

	if got := recoverWorkerEffort(cfg, dir, "no-such-session"); got != nil {
		t.Errorf("got %+v for a session with no transcript, want nil", got)
	}
	if got := recoverWorkerEffort(cfg, dir, ""); got != nil {
		t.Errorf("got %+v with no session id, want nil", got)
	}
}

// TestSetSubEffortByID: one call site serves a standalone run (a single sub) and
// an epic manager's shared run-state, so it must find the sub by ID and touch no
// other entry.
func TestSetSubEffortByID(t *testing.T) {
	run := &storage.RunState{Subs: []storage.SubRun{
		{ID: "p-1-1", Status: "merged"},
		{ID: "p-1-2", Status: "failed"},
	}}
	effort := &storage.WorkerEffort{Source: effortSourceTranscript, Turns: 44, OutputTokens: 90000}
	setSubEffort(run, "p-1-2", effort)
	if run.Subs[0].Effort != nil {
		t.Error("the other sub must be untouched")
	}
	if run.Subs[1].Effort == nil || run.Subs[1].Effort.Turns != 44 {
		t.Fatalf("Subs[1].Effort = %+v, want the recorded effort", run.Subs[1].Effort)
	}
	setSubEffort(run, "not-here", effort) // must not panic or invent an entry
	if len(run.Subs) != 2 {
		t.Errorf("Subs grew to %d - an unknown id must not add an entry", len(run.Subs))
	}
}

// TestDescribeEffortSaysCostIsUnknown: the number travels with its caveat or it
// will be read as "this sub was free".
func TestDescribeEffortSaysCostIsUnknown(t *testing.T) {
	if got := describeEffort(nil); got != "" {
		t.Errorf("describeEffort(nil) = %q, want empty", got)
	}
	got := describeEffort(&storage.WorkerEffort{Source: effortSourceTranscript, Turns: 61, OutputTokens: 120000})
	for _, want := range []string{"61 turn(s)", "120000 output token(s)", "cost is unknown, not zero"} {
		if !strings.Contains(got, want) {
			t.Errorf("describeEffort() = %q, missing %q", got, want)
		}
	}
}

// TestExecuteWorkDeadWorkerJournalsItsEffort is the wiring, end to end on the
// real plumbing: a worker that exits 1 after doing work must leave a journal
// line and a run-state entry that say how much work that was. Evidence for why
// this matters: orbit-112-2 ran 3749 s, pushed its branch, and journaled
// turns 0 / cost 0 - its transcript holds 164 turns and 282100 output tokens.
func TestExecuteWorkDeadWorkerJournalsItsEffort(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	fakeClaude(t, "exit 1")
	store, task, plan, opts := executorFixture(t)

	// The transcript the dying worker left behind. pm can find it because pm
	// minted the session id (plan.sessionID) and pinned it with --session-id.
	path := filepath.Join(cfg, "projects", encodeProjectPath(plan.workDir), plan.sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	tr := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"# Task"}}`,
		assistantUsage(4000, false),
		userTurn(false),
		assistantUsage(6000, false),
	}, "\n")
	if err := os.WriteFile(path, []byte(tr), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := executeWork(store, task, plan, opts); err == nil {
		t.Fatal("a worker that exits 1 must surface an error")
	}

	entries, _ := storage.ReadJournal(store.ProjectDir("app"))
	if len(entries) != 2 || len(entries[1].Subs) != 1 {
		t.Fatalf("journal: %+v", entries)
	}
	sub := entries[1].Subs[0]
	if sub.Turns != 0 || sub.CostUSD != 0 {
		t.Errorf("envelope stats must stay zero - there was no envelope: turns=%d cost=%v", sub.Turns, sub.CostUSD)
	}
	if sub.Effort == nil {
		t.Fatal("journal sub carries no reconstructed effort - the dead worker's hour is invisible again")
	}
	if sub.Effort.Turns != 2 || sub.Effort.OutputTokens != 10000 || sub.Effort.Source != effortSourceTranscript {
		t.Errorf("journal sub effort = %+v, want 2 turns / 10000 tokens from the transcript", sub.Effort)
	}

	run, err := storage.ReadRunState(store.ProjectDir("app"), "app-1")
	if err != nil || len(run.Subs) != 1 {
		t.Fatalf("run-state: %+v err=%v", run, err)
	}
	if run.Subs[0].Effort == nil || run.Subs[0].Effort.Turns != 2 {
		t.Errorf("run-state sub effort = %+v, want the same numbers (this is what an epic manager lifts into its journal)", run.Subs[0].Effort)
	}
}

// TestJournalSubsCarryReconstructedEffort: in an epic the manager writes the
// journal, and it only knows what the run-state holds - so the field has to
// travel from there into the line, or every dead sub in an epic stays invisible
// no matter how well executeWork measured it.
func TestJournalSubsCarryReconstructedEffort(t *testing.T) {
	outcomes := []subOutcome{
		{"p-1-1", "merged", "ok", "feat/one"},
		{"p-1-2", "failed", "claude worker failed: exit status 1", "feat/two"},
	}
	run := &storage.RunState{Subs: []storage.SubRun{
		{ID: "p-1-1", Status: "merged", Turns: 42, CostUSD: 1.25},
		{ID: "p-1-2", Status: "failed", Effort: &storage.WorkerEffort{Source: effortSourceTranscript, Turns: 164, OutputTokens: 282100}},
	}}

	got := journalSubs(outcomes, map[string]int{"p-1-2": 3749}, run)
	if got[0].Effort != nil {
		t.Errorf("a sub with an envelope must carry no reconstructed effort: %+v", got[0].Effort)
	}
	if got[1].Effort == nil || got[1].Effort.Turns != 164 || got[1].Effort.OutputTokens != 282100 {
		t.Fatalf("dead sub effort = %+v, want the run-state's numbers", got[1].Effort)
	}
}

// TestStatsReportsPartialEffort: the rollup has to say that the cost total is a
// floor, next to the total itself - a retro ranks its work by that number.
func TestStatsReportsPartialEffort(t *testing.T) {
	st := aggregateJournal([]storage.JournalEntry{
		{Event: storage.JournalEventStart, TS: "2026-08-11T09:00:00Z", Kind: "run-epic", TaskID: "atlas-112", RunID: "r1", PID: 10},
		{Event: storage.JournalEventEnd, TS: "2026-08-11T11:00:00Z", Kind: "run-epic", TaskID: "atlas-112", RunID: "r1", PID: 10,
			Status: storage.RunStatusDone, Subs: []storage.JournalSub{
				{ID: "atlas-112-1", Result: "merged", Turns: 30, CostUSD: 4.5, DurationS: 900},
				{ID: "atlas-112-2", Result: "failed", DurationS: 3749,
					Effort: &storage.WorkerEffort{Source: effortSourceTranscript, Turns: 161, OutputTokens: 250000}},
			}},
	}, func(int, time.Time) bool { return false }, testNow)

	if st.EffortSubs != 1 || st.EffortTurns != 161 || st.EffortTokens != 250000 {
		t.Fatalf("EffortSubs/Turns/Tokens = %d/%d/%d, want 1/161/250000", st.EffortSubs, st.EffortTurns, st.EffortTokens)
	}
	// The envelope-backed averages must not absorb the reconstructed numbers -
	// mixing the two measurements is what the separate fields exist to prevent.
	if st.Turns.Samples != 1 || st.Turns.Total != 30 {
		t.Errorf("turns stat = %+v, want only the envelope-backed sub", st.Turns)
	}
	out := renderJournalStats("atlas", "/tmp/j.jsonl", st)
	for _, want := range []string{"1 sub(s) died with no envelope", "161 turn(s)", "250000 output token(s)", "FLOOR"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output missing %q:\n%s", want, out)
		}
	}
}
