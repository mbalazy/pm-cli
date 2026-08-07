package cmd

import (
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
)

// The reviewer cap moves ONE decision out of the prompt and into pm: how many
// reviewer subagents a change is worth. The rule itself is not new - it has been
// in genericPhasePrompt(PhaseReview) since 0.36.2, phrased as an instruction to
// count the production diff and size the review to it. What is new is who
// applies it, and that is the entire point.
//
// The evidence that the instruction alone does not work:
//   - orbit-106-3 delivered ONE markdown file (by the rule: 1 reviewer)
//     and spawned 3 in round one, then 1 in each of two further rounds.
//   - the 0.36.2 A/B on a throwaway repo spawned 1 reviewer in BOTH arms with a
//     92-line/2-file diff that the rule put squarely at 3. Recorded in CLAUDE.md
//     at the time as "the model follows this sizing instruction loosely".
//
// Loose in both directions is the tell: this is not a threshold that needs
// tuning, it is a decision that needs an owner that cannot decline to make it.

// productionExcludes are the path shapes that do not count toward the review
// size. Carried over verbatim from the 0.36.2 prompt rule - only the party
// applying it changes. The exclusion is on the COUNT alone: reviewers still get
// the FULL diff, tests included, because a test encoding the wrong behaviour is
// exactly what review has to catch.
var productionExcludes = []func(name, base string) bool{
	// Test files, in the spellings the prompt rule already named.
	func(_, base string) bool {
		for _, mark := range []string{".test.", ".spec.", ".perf."} {
			if strings.Contains(base, mark) {
				return true
			}
		}
		return strings.HasSuffix(base, "_test.go")
	},
	// Test directories.
	func(name, _ string) bool {
		for _, seg := range strings.Split(name, "/") {
			if seg == "__tests__" || seg == "__snapshots__" || seg == "testdata" {
				return true
			}
		}
		return false
	},
	// Lockfiles: enormous diffs, zero behaviour of their own.
	func(_, base string) bool {
		switch base {
		case "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "go.sum", "Cargo.lock",
			"Gemfile.lock", "poetry.lock", "composer.lock", "Podfile.lock":
			return true
		}
		return false
	},
	// Snapshots and generated output.
	func(name, base string) bool {
		return strings.HasSuffix(base, ".snap") ||
			strings.HasSuffix(base, ".pb.go") ||
			strings.HasSuffix(base, "_generated.go") ||
			strings.HasSuffix(base, ".generated.ts") ||
			strings.Contains(name, "/generated/")
	},
}

// isProductionFile reports whether a changed path counts toward the review size.
func isProductionFile(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	base := path.Base(name)
	for _, excluded := range productionExcludes {
		if excluded(name, base) {
			return false
		}
	}
	return true
}

// Reviewer cap thresholds.
//
// Honest note on where these come from: the journal records outcomes, turns and
// cost per sub, but never diff sizes, so there is no distribution to fit them
// to - the Open Question this ticket carried cannot be answered from the data
// that exists. What IS measured is the cost of getting it wrong: the 16
// reviewers in epic orbit-106 accounted for 55.3M tokens, 58% of the
// whole epic, at roughly 3.5M tokens each. So each step up the ladder has to be
// earned by the change actually being bigger, and the smallest band - which is
// where the pathological case lived (one markdown file, three reviewers) - stays
// exactly where the 0.36.2 rule put it.
//
// The bands are deliberately wide rather than finely graded: a boundary nobody
// can justify is a boundary that invites re-tuning, and the win here is the
// ceiling existing at all, not its exact height.
const (
	reviewSmallLines = 50
	reviewSmallFiles = 2
	reviewLargeLines = 400
	reviewLargeFiles = 10
	reviewMaxAgents  = 3
)

// reviewerCap is how many subagents one round may spawn for a given production
// diff. Never zero: a change with nothing but excluded files still gets one
// reviewer, since "the diff is all lockfiles" is a claim worth checking rather
// than assuming.
func reviewerCap(files, lines int) int {
	switch {
	case files <= reviewSmallFiles && lines <= reviewSmallLines:
		return 1
	case files <= reviewLargeFiles && lines <= reviewLargeLines:
		return 2
	default:
		return reviewMaxAgents
	}
}

// parseNumstat sums added+deleted lines over production files in the output of
// `git diff --numstat`. Binary files report "-" for both counts and contribute
// a file but no lines, which is right: a changed binary is a real change with
// no reviewable text.
func parseNumstat(out string) (files, lines int) {
	for _, row := range strings.Split(out, "\n") {
		cols := strings.Split(strings.TrimSpace(row), "\t")
		if len(cols) < 3 {
			continue
		}
		name := cols[len(cols)-1]
		// Rename rows are "old => new" or a 4-column form; the last column is
		// still the path that matters.
		if i := strings.LastIndex(name, " => "); i >= 0 {
			name = name[i+4:]
			name = strings.TrimSuffix(name, "}")
		}
		if !isProductionFile(name) {
			continue
		}
		files++
		added, _ := strconv.Atoi(cols[0])
		deleted, _ := strconv.Atoi(cols[1])
		lines += added + deleted
	}
	return files, lines
}

// diffStats measures the worker's own changes: everything between the commit
// the worker started from and the working tree as it stands, so both committed
// and uncommitted work counts. baseSHA is pinned by pm right before the worker
// spawns, which is what makes this exact - a branch name would drift, and a
// merge-base against an unknown trunk cannot be computed reliably from inside a
// hook.
// Untracked files count too (see gitDiffAll): `git diff` reads the index and
// the tree, not the directory, so a worker that spawns its reviewers before
// committing would otherwise be sized as having changed nothing - and a cap of
// 1 on a 40-file change is a worse failure than no cap at all.
func diffStats(dir, baseSHA string) (files, lines int, ok bool) {
	if dir == "" || baseSHA == "" {
		return 0, 0, false
	}
	c := exec.Command("git", "rev-parse", "--git-dir")
	c.Dir = dir
	if c.Run() != nil {
		return 0, 0, false
	}
	files, lines = parseNumstat(gitDiffAll(dir, baseSHA, "--numstat"))
	return files, lines, true
}

// spawnsThisRound counts the spawns already recorded in the round in progress.
// A round is a cluster of spawns (see storage's reviewRoundGap): the worker
// issues a batch in one turn, then a reviewer actually runs, which takes
// minutes. Nested spawns never count - they are refused outright.
func spawnsThisRound(spawns []storage.ReviewSpawn, now time.Time, gap time.Duration) int {
	n := 0
	for _, s := range spawns {
		// A nested spawn is refused outright, and a refused spawn never ran -
		// neither may consume a budget that exists to bound work actually done.
		if s.Nested || s.Denied {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, s.TS)
		if err != nil {
			continue
		}
		if now.Sub(ts) <= gap {
			n++
		}
	}
	return n
}

// capDenyMessage is what a refused spawn tells the worker. It names the number
// and where it came from, because a bare refusal sends a model hunting for
// another door - the lesson the hook guard's own deny message was built on.
func capDenyMessage(cap, files, lines int) string {
	return "pm review cap: this round already spawned its " + strconv.Itoa(cap) +
		" allowed subagent(s). pm sizes the review itself from the production diff (" +
		strconv.Itoa(files) + " file(s), " + strconv.Itoa(lines) + " changed line(s)), " +
		"so this is a budget, not a judgement about your change - the reviewers you already have " +
		"are the review. Wait for them, act on their findings, and continue. " +
		"If they were not enough, say so in `unresolved` rather than spawning more."
}

// nestedDenyMessage refuses a spawn made from inside a subagent. This is the
// case a cap on the worker cannot see: in orbit-106-3 one reviewer
// spawned its own Explore subagent that made 65 tool calls and burned 6.1M
// tokens, entirely outside any budget the worker was subject to.
const nestedDenyMessage = "pm review cap: a subagent may not spawn further subagents. " +
	"Report what you found and what you could not determine; the agent that spawned you decides what to do next."
