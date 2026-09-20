package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mbalazy/pm-cli/internal/storage"
)

// commitFile commits one file so the repo gains a real commit to count.
func commitFile(t *testing.T, repo, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "add", ".")
	gitT(t, repo, "commit", "-q", "-m", name)
	return gitHeadSHA(repo)
}

// TestCountUnreviewedCommits is the whole point of the change: after a capped
// review loop, "the findings are still open" and "the last fix was never
// reviewed" both come back as `blocked`, and only one of them is about commits
// nobody looked at.
func TestCountUnreviewedCommits(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo)
	reviewed := commitFile(t, repo, "a.txt")

	t.Run("no commits after the last review", func(t *testing.T) {
		tel := &storage.ReviewTelemetry{Spawns: 2, Rounds: 2, LastReviewedHead: reviewed}
		countUnreviewedCommits(repo, tel)
		if tel.UnreviewedCommits != 0 {
			t.Fatalf("UnreviewedCommits = %d, want 0 - the reviewer saw the tip", tel.UnreviewedCommits)
		}
	})

	t.Run("two commits after the last review", func(t *testing.T) {
		commitFile(t, repo, "b.txt")
		commitFile(t, repo, "c.txt")
		tel := &storage.ReviewTelemetry{Spawns: 2, Rounds: 2, LastReviewedHead: reviewed}
		countUnreviewedCommits(repo, tel)
		if tel.UnreviewedCommits != 2 {
			t.Fatalf("UnreviewedCommits = %d, want 2", tel.UnreviewedCommits)
		}
	})

	// Every degraded path leaves the number at zero rather than guessing: the
	// count is evidence, and a fabricated one is worse than none.
	t.Run("degraded paths stay silent", func(t *testing.T) {
		for name, tel := range map[string]*storage.ReviewTelemetry{
			"no telemetry at all":     nil,
			"no head recorded":        {Spawns: 1},
			"head not in this repo":   {Spawns: 1, LastReviewedHead: "0123456789012345678901234567890123456789"},
			"head is not a commit id": {Spawns: 1, LastReviewedHead: "not-a-sha"},
		} {
			countUnreviewedCommits(repo, tel)
			if tel != nil && tel.UnreviewedCommits != 0 {
				t.Errorf("%s: UnreviewedCommits = %d, want 0", name, tel.UnreviewedCommits)
			}
		}
		tel := &storage.ReviewTelemetry{Spawns: 1, LastReviewedHead: reviewed}
		countUnreviewedCommits("", tel)
		countUnreviewedCommits(t.TempDir(), tel) // a dir that is not a repo
		if tel.UnreviewedCommits != 0 {
			t.Errorf("UnreviewedCommits = %d, want 0 without a usable repo", tel.UnreviewedCommits)
		}
	})
}

// TestLastReviewedHeadIsTheLastAllowedSpawn: a refused spawn never ran, so its
// head is not a tip anything reviewed - counting it would report the branch as
// reviewed further than it was, which is the failure direction that matters.
func TestLastReviewedHeadIsTheLastAllowedSpawn(t *testing.T) {
	tel := storage.AggregateReviewSpawns([]storage.ReviewSpawn{
		{TS: "2026-08-11T10:00:00.0Z", Head: "aaa"},
		{TS: "2026-08-11T10:20:00.0Z", Head: "bbb", SubagentType: "general-purpose"},
		{TS: "2026-08-11T10:40:00.0Z", Head: "ccc", Denied: true},
		{TS: "2026-08-11T10:41:00.0Z", Head: "ddd", Nested: true},
		// A custom-type spawn is untouched by the review harness (no packet, no
		// model pin) - a late explore/helper agent must not zero the
		// unreviewed-commits signal by "seeing" the post-fix tip.
		{TS: "2026-08-11T10:42:00.0Z", Head: "eee", SubagentType: "my-custom-agent"},
	})
	if tel.LastReviewedHead != "bbb" {
		t.Fatalf("LastReviewedHead = %q, want bbb (the last top-level spawn pm treats as a reviewer)", tel.LastReviewedHead)
	}
}

// TestStatsReportsUnreviewedCommits: the number has to be readable without
// opening the journal by hand, next to the rounds it belongs with.
func TestStatsReportsUnreviewedCommits(t *testing.T) {
	st := aggregateJournal([]storage.JournalEntry{
		{Event: storage.JournalEventStart, TS: "2026-08-11T09:00:00Z", Kind: "run-epic", TaskID: "orbit-114", RunID: "r1", PID: 10},
		{Event: storage.JournalEventEnd, TS: "2026-08-11T10:00:00Z", Kind: "run-epic", TaskID: "orbit-114", RunID: "r1", PID: 10,
			Status: storage.RunStatusDone, Subs: []storage.JournalSub{
				{ID: "orbit-114-1", Result: "blocked", Turns: 120, CostUSD: 23.75,
					Review: &storage.ReviewTelemetry{Spawns: 4, Rounds: 2, LastReviewedHead: "abc", UnreviewedCommits: 1}},
				{ID: "orbit-114-2", Result: "merged", Turns: 20,
					Review: &storage.ReviewTelemetry{Spawns: 2, Rounds: 1, LastReviewedHead: "def"}},
			}},
	}, func(int, time.Time) bool { return false }, testNow)

	if st.UnreviewedSubs != 1 || st.UnreviewedCommits != 1 {
		t.Fatalf("UnreviewedSubs/Commits = %d/%d, want 1/1", st.UnreviewedSubs, st.UnreviewedCommits)
	}
	out := renderJournalStats("orbit", "/tmp/j.jsonl", st)
	if !strings.Contains(out, "unreviewed 1 sub(s) landed 1 commit(s) after their last review") {
		t.Errorf("stats does not report unreviewed commits:\n%s", out)
	}
}
