package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

func TestIsProductionFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"internal/cmd/work.go", true},
		{"src/components/Button.tsx", true},
		{"docs/ACME-1574/sim-test-plan.md", true}, // the orbit-106-3 change: 1 file, 1 reviewer
		{"README.md", true},
		{"Makefile", true},

		{"internal/cmd/work_test.go", false},
		{"src/Button.test.tsx", false},
		{"src/Button.spec.ts", false},
		{"src/list.perf.ts", false},
		{"src/__tests__/thing.ts", false},
		{"src/__snapshots__/thing.snap", false},
		{"internal/storage/testdata/fixture.md", false},
		{"yarn.lock", false},
		{"package-lock.json", false},
		{"go.sum", false},
		{"ios/Podfile.lock", false},
		{"api/service.pb.go", false},
		{"src/generated/types.ts", false},
		{"src/api.generated.ts", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isProductionFile(c.name); got != c.want {
			t.Errorf("isProductionFile(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReviewerCap(t *testing.T) {
	cases := []struct {
		name         string
		files, lines int
		want         int
	}{
		// The case the whole ticket exists for: orbit-106-3 delivered one
		// markdown file and got 3 reviewers in round one alone.
		{"one markdown file", 1, 40, 1},
		{"nothing production left after exclusions", 0, 0, 1},
		{"right at the small boundary", 2, 50, 1},
		{"one line over the small line budget", 2, 51, 2},
		{"one file over the small file budget", 3, 10, 2},
		{"right at the large boundary", 10, 400, 2},
		{"over the large line budget", 3, 401, 3},
		{"over the large file budget", 11, 100, 3},
		{"very large", 40, 5000, 3},
	}
	for _, c := range cases {
		if got := reviewerCap(c.files, c.lines); got != c.want {
			t.Errorf("%s: reviewerCap(%d, %d) = %d, want %d", c.name, c.files, c.lines, got, c.want)
		}
	}
	// The ceiling is what bounds the cost; nothing may exceed it.
	if got := reviewerCap(1000, 1000000); got > reviewMaxAgents {
		t.Errorf("cap must never exceed %d, got %d", reviewMaxAgents, got)
	}
}

func TestParseNumstatCountsProductionOnly(t *testing.T) {
	// Shape straight out of `git diff --numstat`, including a binary row and a
	// rename row.
	out := strings.Join([]string{
		"10\t2\tinternal/cmd/work.go",
		"74\t0\tinternal/cmd/work_test.go", // tests: excluded from the COUNT
		"3\t1\tsrc/Button.tsx",
		"2000\t1500\tyarn.lock", // lockfile: excluded
		"-\t-\tassets/logo.png", // binary: a file, no lines
		"5\t5\tsrc/{old => new}/thing.ts",
	}, "\n")
	files, lines := parseNumstat(out)
	if files != 4 {
		t.Errorf("files = %d, want 4 (work.go, Button.tsx, logo.png, thing.ts)", files)
	}
	if lines != 26 {
		t.Errorf("lines = %d, want 26 (12 + 4 + 0 + 10)", lines)
	}
	// The exact failure the 0.36.2 note describes: 16 production lines plus a
	// 74-line test file must not read as a large change.
	if got := reviewerCap(parseNumstat("16\t0\tsrc/copy.ts\n74\t0\tsrc/copy.test.ts")); got != 1 {
		t.Errorf("a 16-line copy fix with a 74-line test = %d reviewers, want 1", got)
	}
}

func TestSpawnsThisRound(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	spawns := []storage.ReviewSpawn{
		{TS: at(-10 * time.Minute)},              // a previous round
		{TS: at(-9 * time.Minute)},               // a previous round
		{TS: at(-2 * time.Second)},               // this round
		{TS: at(-1 * time.Second)},               // this round
		{TS: at(-1 * time.Second), Nested: true}, // never counts
		{TS: at(-1 * time.Second), Denied: true}, // refused, so it never ran
		{TS: "garbage"},
	}
	if got := spawnsThisRound(spawns, now, storage.ReviewRoundGap); got != 2 {
		t.Errorf("spawnsThisRound = %d, want 2", got)
	}
}

// The end-to-end decision, against a REAL git repo: the cap is only worth
// anything if it can measure a diff the way git reports one.
func TestJudgeAgentSpawnEnforcesTheCap(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "T")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", "package main\n")
	git("add", "-A")
	git("commit", "-qm", "init")

	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = dir
	shaOut, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(shaOut))

	// A small production change plus a big test file: cap 1, and the test file
	// must not push it higher (the 0.36.2 miscount).
	write("main.go", "package main\n\nfunc add(a, b int) int { return a + b }\n")
	write("main_test.go", strings.Repeat("// filler\n", 90))

	telemetry := filepath.Join(dir, "review.jsonl")
	ev := hookEvent{ToolName: "Agent", Cwd: dir}
	ev.ToolInput.SubagentType = "Explore"
	ev.ToolInput.Prompt = "review this"

	now := time.Now()
	if reason := judgeAgentSpawn(telemetry, base, ev, now); reason != "" {
		t.Fatalf("first spawn must be allowed, got %q", reason)
	}
	reason := judgeAgentSpawn(telemetry, base, ev, now.Add(time.Second))
	if reason == "" {
		t.Fatal("second spawn in the same round must be refused for a 1-file, 2-line change")
	}
	// The refusal has to tell the worker what to do, not just say no - the same
	// rule the hook-bypass deny message follows.
	for _, want := range []string{"unresolved", "1 allowed subagent"} {
		if !strings.Contains(reason, want) {
			t.Errorf("deny message must contain %q, got %q", want, reason)
		}
	}
	// A spawn far enough later is a NEW round and gets its budget back.
	if reason := judgeAgentSpawn(telemetry, base, ev, now.Add(5*time.Minute)); reason != "" {
		t.Errorf("a new round must get a fresh budget, got %q", reason)
	}

	spawns, err := storage.ReadReviewSpawns(telemetry)
	if err != nil {
		t.Fatal(err)
	}
	if len(spawns) != 3 {
		t.Fatalf("every spawn must be recorded, refused included; got %d", len(spawns))
	}
	agg := storage.AggregateReviewSpawns(spawns)
	if agg.Spawns != 2 || agg.Denied != 1 {
		t.Errorf("aggregate = %+v, want 2 spawns and 1 denial (a refusal never ran)", agg)
	}
	if agg.Rounds != 2 {
		t.Errorf("rounds = %d, want 2", agg.Rounds)
	}
}

// Without a base sha the cap cannot be sized, and an unsized cap must let the
// worker work: this guards tokens, not correctness, so guessing would be the
// worse failure.
func TestJudgeAgentSpawnDegradesWhenItCannotMeasure(t *testing.T) {
	dir := t.TempDir()
	telemetry := filepath.Join(dir, "review.jsonl")
	ev := hookEvent{ToolName: "Agent", Cwd: dir}
	for i := 0; i < 5; i++ {
		if reason := judgeAgentSpawn(telemetry, "", ev, time.Now()); reason != "" {
			t.Fatalf("spawn %d refused with no diff base: %q", i, reason)
		}
	}
	// Not a git repo at all - git fails, same degradation.
	if reason := judgeAgentSpawn(telemetry, "deadbeef", ev, time.Now()); reason != "" {
		t.Errorf("a failing git must degrade to allow, got %q", reason)
	}
}

// The prompt must stop stating a number the model cannot influence, and must
// say who owns the count instead.
func TestReviewPromptDelegatesTheCount(t *testing.T) {
	p := genericPhasePrompt(storage.PhaseReview)
	for _, gone := range []string{"spawn 1 independent reviewer", "anything larger -> spawn 3", "<~50 changed lines"} {
		if strings.Contains(p, gone) {
			t.Errorf("review prompt must no longer state the sizing rule, still contains %q", gone)
		}
	}
	for _, want := range []string{"Do NOT decide how many", "pm sizes the review", "FULL diff"} {
		if !strings.Contains(p, want) {
			t.Errorf("review prompt must contain %q", want)
		}
	}
}

// A JSON payload shaped like the real one, exercised through the command entry
// point rather than the helper, so the flag wiring is covered too.
func TestWorkerGuardCapThroughCommand(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"tool_name": "Agent",
		"tool_input": map[string]any{
			"subagent_type": "Explore",
			"prompt":        "audit this",
		},
		"agent_id":   "abc123",
		"agent_type": "Explore",
	})
	var errOut bytes.Buffer
	code := runWorkerGuard(bytes.NewReader(payload), &errOut, filepath.Join(t.TempDir(), "t.jsonl"), "")
	if code != 2 {
		t.Fatalf("nested spawn exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "may not spawn further subagents") {
		t.Errorf("deny reason = %q", errOut.String())
	}
}
