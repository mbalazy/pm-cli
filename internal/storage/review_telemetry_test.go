package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ts renders an offset from a fixed instant in the format the hook writes.
func ts(offset time.Duration) string {
	base := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	return base.Add(offset).Format(time.RFC3339Nano)
}

func TestAggregateReviewSpawns(t *testing.T) {
	cases := []struct {
		name   string
		spawns []ReviewSpawn
		want   ReviewTelemetry
	}{
		{
			name:   "no spawns is a real result, not an absence",
			spawns: nil,
			want:   ReviewTelemetry{Spawns: 0, Rounds: 0},
		},
		{
			// The measured shape of a real review phase: parallel spawns land
			// under a second apart, a new round costs a whole reviewer run.
			name: "parallel spawns in one turn are one round",
			spawns: []ReviewSpawn{
				{TS: ts(0), Model: "opus"},
				{TS: ts(600 * time.Millisecond), Model: "opus"},
				{TS: ts(900 * time.Millisecond), Model: "opus"},
			},
			want: ReviewTelemetry{Spawns: 3, Rounds: 1, Models: "opus"},
		},
		{
			name: "a gap larger than the threshold starts a new round",
			spawns: []ReviewSpawn{
				{TS: ts(0), Model: "opus"},
				{TS: ts(500 * time.Millisecond), Model: "opus"},
				{TS: ts(4 * time.Minute), Model: "opus"},
				{TS: ts(9 * time.Minute), Model: "opus"},
			},
			want: ReviewTelemetry{Spawns: 4, Rounds: 3, Models: "opus"},
		},
		{
			// A nested spawn happens WHILE a reviewer runs, so it is not a
			// review->fix round and must not inflate the count.
			name: "nested spawns count separately and never open a round",
			spawns: []ReviewSpawn{
				{TS: ts(0), Model: "sonnet"},
				{TS: ts(3 * time.Minute), Nested: true, AgentType: "Explore", Model: "sonnet"},
			},
			want: ReviewTelemetry{Spawns: 2, Rounds: 1, Nested: 1, Models: "sonnet"},
		},
		{
			// The pm-cli-105 measurement: spawns the worker asked to background
			// (or left to the tool's background default) are counted, before
			// pm's forced-sync correction.
			name: "background asks are counted",
			spawns: []ReviewSpawn{
				{TS: ts(0), Model: "opus", Background: true},
				{TS: ts(700 * time.Millisecond), Model: "opus"},
			},
			want: ReviewTelemetry{Spawns: 2, Rounds: 1, Background: 1, Models: "opus"},
		},
		{
			// An unnamed model is a different fact from a named one: the
			// caller-side model beats an agent definition's, so "inherit" is
			// exactly what a definition-only pin would leave behind.
			name: "an unnamed model is recorded as inherit",
			spawns: []ReviewSpawn{
				{TS: ts(0)},
				{TS: ts(200 * time.Millisecond), Model: "opus"},
			},
			want: ReviewTelemetry{Spawns: 2, Rounds: 1, Models: "inherit,opus"},
		},
		{
			name: "diff-carrying prompts are counted",
			spawns: []ReviewSpawn{
				{TS: ts(0), Model: "sonnet", HasDiff: true},
				{TS: ts(100 * time.Millisecond), Model: "sonnet"},
			},
			want: ReviewTelemetry{Spawns: 2, Rounds: 1, WithDiff: 1, Models: "sonnet"},
		},
		{
			// Same lesson the journal's ordering rules learned: a bad timestamp
			// must not sort to an end and invent a boundary.
			name: "an unparseable timestamp drops out of clustering",
			spawns: []ReviewSpawn{
				{TS: "not a timestamp", Model: "opus"},
				{TS: ts(0), Model: "opus"},
				{TS: ts(400 * time.Millisecond), Model: "opus"},
			},
			want: ReviewTelemetry{Spawns: 3, Rounds: 1, Models: "opus"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AggregateReviewSpawns(c.spawns)
			if got == nil {
				t.Fatal("AggregateReviewSpawns must always return a value")
			}
			if *got != c.want {
				t.Errorf("got %+v, want %+v", *got, c.want)
			}
		})
	}
}

func TestReviewTelemetryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".executor", "review-sess.jsonl")
	for _, s := range []ReviewSpawn{
		{TS: ts(0), Model: "sonnet", SubagentType: "reviewer", HasDiff: true},
		{TS: ts(time.Second), Model: "sonnet", SubagentType: "reviewer"},
	} {
		if err := AppendReviewSpawn(path, s); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	// A corrupt line must be skipped, not abort the read - same rule as
	// ReadJournal, and this file is written by concurrent hook processes.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, err := ReadReviewSpawns(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d spawns, want 2 (corrupt line skipped)", len(got))
	}
	if got[0].SubagentType != "reviewer" || !got[0].HasDiff {
		t.Errorf("fields did not round-trip: %+v", got[0])
	}
	if agg := AggregateReviewSpawns(got); agg.Spawns != 2 || agg.Rounds != 1 {
		t.Errorf("aggregate = %+v, want 2 spawns in 1 round", agg)
	}
}

// The distinction this protects is the reason InitReviewTelemetry exists: an
// absent file means nobody measured, an empty one means the worker spawned
// nobody - and the second is the outcome the whole change is aiming at.
func TestCollectReviewTelemetryDistinguishesUnmeasuredFromZero(t *testing.T) {
	dir := t.TempDir()

	if got := CollectReviewTelemetry(dir, "sess-none"); got != nil {
		t.Errorf("no file must read as unmeasured (nil), got %+v", got)
	}

	path := ReviewTelemetryPath(dir, "sess-zero")
	InitReviewTelemetry(path)
	got := CollectReviewTelemetry(dir, "sess-zero")
	if got == nil {
		t.Fatal("an empty file must read as a measured zero, not as unmeasured")
	}
	if got.Spawns != 0 || got.Rounds != 0 {
		t.Errorf("got %+v, want zeroes", *got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("collect must remove the file so .executor does not grow without bound")
	}

	if got := CollectReviewTelemetry("", "sess"); got != nil {
		t.Error("an unresolvable path must degrade to nil, never panic")
	}
}

// The sidecar calls file rides along with the spawn telemetry: its counts fold
// into the rollup and collect removes it, so .executor does not accumulate one
// stray file per session.
func TestCollectReviewTelemetryFoldsAgentCalls(t *testing.T) {
	dir := t.TempDir()
	path := ReviewTelemetryPath(dir, "sess-calls")
	InitReviewTelemetry(path)
	callsPath := AgentCallsPath(path)
	for _, c := range []AgentToolCall{
		{AgentID: "a", Tool: "Read"},
		{AgentID: "a", Tool: "Grep"},
		{AgentID: "b", Tool: "Glob"},
		{AgentID: "a", Tool: "Read", Denied: true},
	} {
		if err := AppendAgentToolCall(callsPath, c); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	got := CollectReviewTelemetry(dir, "sess-calls")
	if got == nil {
		t.Fatal("telemetry file exists - must not read as unmeasured")
	}
	if got.ToolCalls != 3 || got.ToolDenied != 1 {
		t.Errorf("ToolCalls=%d ToolDenied=%d, want 3 and 1", got.ToolCalls, got.ToolDenied)
	}
	if _, err := os.Stat(callsPath); !os.IsNotExist(err) {
		t.Error("collect must remove the calls sidecar too")
	}
}

func TestCountAgentToolCalls(t *testing.T) {
	calls := []AgentToolCall{
		{AgentID: "a", Tool: "Read"},
		{AgentID: "b", Tool: "Read"},
		{AgentID: "a", Tool: "Grep", Denied: true},
		{AgentID: "a", Tool: "Glob"},
	}
	if got := CountAgentToolCalls(calls, "a"); got != 2 {
		t.Errorf("agent a = %d, want 2 (denied calls never consume budget)", got)
	}
	if got := CountAgentToolCalls(calls, "b"); got != 1 {
		t.Errorf("agent b = %d, want 1", got)
	}
	if got := CountAgentToolCalls(nil, "a"); got != 0 {
		t.Errorf("empty set = %d, want 0", got)
	}
}
