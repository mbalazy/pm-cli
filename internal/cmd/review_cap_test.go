package cmd

import (
	"bytes"
	"encoding/json"
	"io"
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
		docOnly      bool
		want         int
	}{
		// The case the whole ticket exists for: orbit-106-3 delivered one
		// markdown file and got 3 reviewers in round one alone.
		{"one markdown file", 1, 40, true, 1},
		{"nothing production left after exclusions", 0, 0, false, 1},
		{"right at the small boundary", 2, 50, false, 1},
		{"one line over the small line budget", 2, 51, false, 2},
		{"one file over the small file budget", 3, 10, false, 2},
		{"right at the large boundary", 10, 400, false, 2},
		{"over the large line budget", 3, 401, false, 3},
		{"over the large file budget", 11, 100, false, 3},
		{"very large", 40, 5000, false, 3},
		// The size the bands would have priced at 3 - and the actual size of the
		// document that cost $40.38. Prose is longer, not riskier.
		{"one 656-line markdown file", 1, 656, true, 1},
		{"a whole documentation set", 12, 4000, true, 1},
	}
	for _, c := range cases {
		if got := reviewerCap(c.files, c.lines, c.docOnly); got != c.want {
			t.Errorf("%s: reviewerCap(%d, %d, docOnly=%v) = %d, want %d", c.name, c.files, c.lines, c.docOnly, got, c.want)
		}
	}
	// The ceiling is what bounds the cost; nothing may exceed it.
	if got := reviewerCap(1000, 1000000, false); got > reviewMaxAgents {
		t.Errorf("cap must never exceed %d, got %d", reviewMaxAgents, got)
	}
}

func TestProseClassification(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"docs/ACME-1574/sim-test-plan.md", true},
		{"README.MD", true},
		{"notes.mdx", true},
		{"CHANGELOG.txt", true},
		{"doc/guide.rst", true},
		{"internal/cmd/work.go", false},
		{"src/Button.tsx", false},
		{"docs/example.ts", false}, // under docs/, still code
		{"Makefile", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isProseFile(c.name); got != c.want {
			t.Errorf("isProseFile(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDocOnlyIsAClaimAboutTheWholeChange(t *testing.T) {
	cases := []struct {
		name    string
		numstat string
		want    bool
	}{
		{"one document", "656\t0\tdocs/plan.md", true},
		{"several documents", "20\t3\tREADME.md\n5\t0\tdocs/a.rst", true},
		{"a document and a code file", "20\t3\tREADME.md\n2\t1\tsrc/a.ts", false},
		// A test file is excluded from the SIZE, but it is still code, and a
		// change that touches one is not a documentation change.
		{"a document and a test file", "20\t3\tREADME.md\n40\t0\tsrc/a.test.ts", false},
		{"nothing changed", "", false},
	}
	for _, c := range cases {
		if _, _, got := parseNumstat(c.numstat); got != c.want {
			t.Errorf("%s: docOnly = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEffectiveFixRoundsOnlyLowers(t *testing.T) {
	cases := []struct {
		configured int
		docOnly    bool
		want       int
	}{
		{2, true, 1},  // the default, on a document
		{3, true, 1},  // a project that raised it, on a document
		{1, true, 1},  // already there
		{0, true, 0},  // rounds switched off stay off - a doc must not turn them on
		{2, false, 2}, // code is untouched
		{3, false, 3},
	}
	for _, c := range cases {
		if got := effectiveFixRounds(c.configured, c.docOnly); got != c.want {
			t.Errorf("effectiveFixRounds(%d, docOnly=%v) = %d, want %d", c.configured, c.docOnly, got, c.want)
		}
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
	files, lines, _ := parseNumstat(out)
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

	// OUTSIDE the repo, as in production (the pm data dir): untracked files
	// inside the repo are part of the worker's change and count toward the cap,
	// so a telemetry file sitting in the tree would size the review against pm's
	// own bookkeeping.
	telemetry := filepath.Join(t.TempDir(), "review.jsonl")
	ev := hookEvent{ToolName: "Agent", Cwd: dir}
	ev.ToolInput.SubagentType = "Explore"
	ev.ToolInput.Prompt = "review this"

	now := time.Now()
	if reason := judgeAgentSpawn(guardOptions{telemetryPath: telemetry, diffBase: base}, ev, now); reason != "" {
		t.Fatalf("first spawn must be allowed, got %q", reason)
	}
	reason := judgeAgentSpawn(guardOptions{telemetryPath: telemetry, diffBase: base}, ev, now.Add(time.Second))
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
	if reason := judgeAgentSpawn(guardOptions{telemetryPath: telemetry, diffBase: base}, ev, now.Add(5*time.Minute)); reason != "" {
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
	// Each recorded spawn carries the tip that reviewer could see, which is what
	// makes "the last fix was never reviewed" countable afterwards (see
	// countUnreviewedCommits). The head here is the base commit: the worker's
	// changes are still uncommitted.
	for i, sp := range spawns {
		if sp.Head != base {
			t.Errorf("spawn %d recorded head %q, want the repo tip %q", i, sp.Head, base)
		}
	}
	if agg.LastReviewedHead != base {
		t.Errorf("LastReviewedHead = %q, want %q", agg.LastReviewedHead, base)
	}
}

// The contrast with the test above, on the same shape: prose instead of code.
// The second reviewer of the round is refused there too, but here so is the new
// ROUND five minutes later - which for a code change is explicitly allowed. The
// document is 656 lines, the size of the one that cost $40.38, so this also
// pins that the bands no longer reach it.
func TestJudgeAgentSpawnGivesADocumentOneReviewerAndOneRound(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "init")
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = dir
	shaOut, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(shaOut))

	// Untracked, as it is at the moment a worker spawns its reviewers: it has
	// written the document and not necessarily committed it yet.
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "plan.md"),
		[]byte(strings.Repeat("a line of the test plan\n", 656)), 0o644); err != nil {
		t.Fatal(err)
	}

	telemetry := filepath.Join(t.TempDir(), "review.jsonl")
	ev := hookEvent{ToolName: "Agent", Cwd: dir}
	ev.ToolInput.SubagentType = "Explore"
	ev.ToolInput.Prompt = "review this"
	// fix_rounds at its default: the doc regime has to lower it, not read it.
	opts := guardOptions{telemetryPath: telemetry, diffBase: base, fixRounds: 2}

	now := time.Now()
	if reason := judgeAgentSpawn(opts, ev, now); reason != "" {
		t.Fatalf("the first reviewer must be allowed, got %q", reason)
	}
	reason := judgeAgentSpawn(opts, ev, now.Add(time.Second))
	if reason == "" {
		t.Fatal("a 656-line document must not get a second reviewer")
	}
	for _, want := range []string{"documentation only", "unresolved"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the count refusal must say %q, got %q", want, reason)
		}
	}
	// The round cap is the half the count cap cannot do: on code this spawn
	// opens a fresh round with a fresh budget.
	reason = judgeAgentSpawn(opts, ev, now.Add(5*time.Minute))
	if reason == "" {
		t.Fatal("a document gets one round; the second must be refused")
	}
	if !strings.Contains(reason, "single review round") {
		t.Errorf("the round refusal must say why, got %q", reason)
	}
}

// Without a base sha the cap cannot be sized, and an unsized cap must let the
// worker work: this guards tokens, not correctness, so guessing would be the
// worse failure.
func TestJudgeAgentSpawnDegradesWhenItCannotMeasure(t *testing.T) {
	dir := t.TempDir()
	// OUTSIDE the repo, as in production (the pm data dir): untracked files
	// inside the repo are part of the worker's change and count toward the cap,
	// so a telemetry file sitting in the tree would size the review against pm's
	// own bookkeeping.
	telemetry := filepath.Join(t.TempDir(), "review.jsonl")
	ev := hookEvent{ToolName: "Agent", Cwd: dir}
	for i := 0; i < 5; i++ {
		if reason := judgeAgentSpawn(guardOptions{telemetryPath: telemetry}, ev, time.Now()); reason != "" {
			t.Fatalf("spawn %d refused with no diff base: %q", i, reason)
		}
	}
	// Not a git repo at all - git fails, same degradation.
	if reason := judgeAgentSpawn(guardOptions{telemetryPath: telemetry, diffBase: "deadbeef"}, ev, time.Now()); reason != "" {
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
	for _, want := range []string{"Do NOT decide how many", "pm sizes the review", "pm attaches the full change"} {
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
	code := runWorkerGuard(bytes.NewReader(payload), io.Discard, &errOut, guardOptions{telemetryPath: filepath.Join(t.TempDir(), "t.jsonl")})
	if code != 2 {
		t.Fatalf("nested spawn exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "may not spawn further subagents") {
		t.Errorf("deny reason = %q", errOut.String())
	}
}

func TestRoundIndex(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	cases := []struct {
		name   string
		spawns []storage.ReviewSpawn
		want   int
	}{
		{"nothing yet is round one", nil, 1},
		{
			"a spawn joining the batch already in flight",
			[]storage.ReviewSpawn{{TS: at(-2 * time.Second)}},
			1,
		},
		{
			"a spawn after the reviewers have run opens round two",
			[]storage.ReviewSpawn{{TS: at(-10 * time.Minute)}, {TS: at(-10 * time.Minute)}},
			2,
		},
		{
			"two rounds behind us, so this is the third",
			[]storage.ReviewSpawn{{TS: at(-20 * time.Minute)}, {TS: at(-10 * time.Minute)}},
			3,
		},
		{
			// Neither reviewed anything, so neither is a round.
			"refused and nested spawns are not rounds",
			[]storage.ReviewSpawn{
				{TS: at(-20 * time.Minute), Denied: true},
				{TS: at(-10 * time.Minute), Nested: true},
			},
			1,
		},
	}
	for _, c := range cases {
		if got := roundIndex(c.spawns, now, storage.ReviewRoundGap); got != c.want {
			t.Errorf("%s: roundIndex = %d, want %d", c.name, got, c.want)
		}
	}
}

// The round cap is the half of this that the prompt alone never delivered: all
// three subs of epic orbit-106 hit the cap of 3, every time, and the
// third round was a confirming pass rather than a fix.
func TestJudgeAgentSpawnEnforcesTheRoundCap(t *testing.T) {
	dir := t.TempDir()
	telemetry := filepath.Join(t.TempDir(), "review.jsonl")
	// No git repo here on purpose: without a measurable diff the reviewer-count
	// cap degrades to allow, so what is left is the round cap alone.
	opts := guardOptions{telemetryPath: telemetry, fixRounds: 2}
	ev := hookEvent{ToolName: "Agent", Cwd: dir}
	ev.ToolInput.SubagentType = reviewerAgentType

	start := time.Now()
	if reason := judgeAgentSpawn(opts, ev, start); reason != "" {
		t.Fatalf("round 1 must be allowed: %q", reason)
	}
	if reason := judgeAgentSpawn(opts, ev, start.Add(5*time.Minute)); reason != "" {
		t.Fatalf("round 2 must be allowed: %q", reason)
	}
	reason := judgeAgentSpawn(opts, ev, start.Add(10*time.Minute))
	if reason == "" {
		t.Fatal("round 3 must be refused with fix_rounds 2")
	}
	// The refusal has to say what to do instead, or the worker just tries again.
	for _, want := range []string{"2 review round", "unresolved"} {
		if !strings.Contains(reason, want) {
			t.Errorf("deny message must contain %q, got %q", want, reason)
		}
	}

	// fix_rounds 0 = unbounded, which is what a hook invoked without the flag
	// sees; it must behave exactly as it did before rounds were capped.
	unbounded := guardOptions{telemetryPath: filepath.Join(t.TempDir(), "r.jsonl")}
	for i := 0; i < 5; i++ {
		if reason := judgeAgentSpawn(unbounded, ev, start.Add(time.Duration(i)*5*time.Minute)); reason != "" {
			t.Fatalf("round %d refused with no cap configured: %q", i+1, reason)
		}
	}
}

// Two things the number alone does not fix: the loop's exit condition, and the
// worker being told a number it can simply keep spending.
func TestReviewLoopExitCondition(t *testing.T) {
	sys := buildWorkerSystemPrompt(storage.Executor{FixRounds: 2}, true, false)
	if strings.Contains(sys, "Repeat review->fix until the review is clean") {
		t.Error("the old exit condition is what made the cap a plan: an adversarial reviewer never returns 'clean'")
	}
	for _, want := range []string{"ONLY IF", "valid finding", "Review->fix round cap: 2."} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt must contain %q", want)
		}
	}
}
