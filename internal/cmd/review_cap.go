package cmd

import (
	"os/exec"
	"path"
	"sort"
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

// proseExtensions are the file shapes whose content is documentation rather
// than code. Deliberately a closed list of extensions rather than a path rule
// like "under docs/": what decides how a change should be reviewed is what the
// file IS, and a .ts file under docs/ is still code.
var proseExtensions = map[string]bool{
	".md": true, ".mdx": true, ".markdown": true,
	".txt": true, ".rst": true, ".adoc": true, ".asciidoc": true, ".org": true,
}

// isProseFile reports whether a changed path is documentation.
func isProseFile(name string) bool {
	return proseExtensions[strings.ToLower(path.Ext(strings.TrimSpace(name)))]
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

// A documentation-only change gets one reviewer and one round, whatever its
// size. The bands above are a proxy for how much can go wrong in a change, and
// on prose the proxy simply does not hold: a long document is longer, not
// riskier, and nothing in it can break at runtime.
//
// This is the case epic pm-cli-96 measured and did not fix. Sub
// orbit-106-3 delivered ONE markdown file of 656 lines - by the bands
// that is 1 file but >400 lines, i.e. cap 3, exactly what the model chose
// unaided - and cost $40.38 of the epic's $89.09. Its transcript says where
// that went: the document was written and committed in 11 minutes, and 43 of
// the sub's 55 minutes were the review loop, whose last two rounds were named
// "Verify round-2 fixes" and "Verify round-3 fixes".
//
// The round ceiling is part of the same decision rather than a separate knob.
// One reviewer with three rounds is the same loop at a slower rate: what ends a
// document review is the reviewer having checked the document's claims, and
// that is one pass.
const docOnlyReviewRounds = 1

// reviewerCap is how many subagents one round may spawn for a given production
// diff. Never zero: a change with nothing but excluded files still gets one
// reviewer, since "the diff is all lockfiles" is a claim worth checking rather
// than assuming.
func reviewerCap(files, lines int, docOnly bool) int {
	if docOnly {
		return 1
	}
	switch {
	case files <= reviewSmallFiles && lines <= reviewSmallLines:
		return 1
	case files <= reviewLargeFiles && lines <= reviewLargeLines:
		return 2
	default:
		return reviewMaxAgents
	}
}

// effectiveFixRounds is how many review rounds a change actually gets. It only
// ever LOWERS the configured number: a project that has switched rounds off
// entirely (0) keeps them off, and one that already allows a single round is
// unchanged.
func effectiveFixRounds(configured int, docOnly bool) int {
	if docOnly && configured > docOnlyReviewRounds {
		return docOnlyReviewRounds
	}
	return configured
}

// parseNumstat sums added+deleted lines over production files in the output of
// `git diff --numstat`. Binary files report "-" for both counts and contribute
// a file but no lines, which is right: a changed binary is a real change with
// no reviewable text.
//
// docOnly is reported over EVERY changed path, not just the production ones:
// the exclusions exist to stop a test file inflating a size, while doc-only is
// a claim about the whole change, and a change that touches a test file is not
// a documentation change however much prose came with it.
func parseNumstat(out string) (files, lines int, docOnly bool) {
	changed, prose := 0, 0
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
		changed++
		if isProseFile(name) {
			prose++
		}
		if !isProductionFile(name) {
			continue
		}
		files++
		added, _ := strconv.Atoi(cols[0])
		deleted, _ := strconv.Atoi(cols[1])
		lines += added + deleted
	}
	return files, lines, changed > 0 && prose == changed
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
func diffStats(dir, baseSHA string) diffSize {
	if dir == "" || baseSHA == "" {
		return diffSize{}
	}
	c := exec.Command("git", "rev-parse", "--git-dir")
	c.Dir = dir
	if c.Run() != nil {
		return diffSize{}
	}
	files, lines, docOnly := parseNumstat(gitDiffAll(dir, baseSHA, "--numstat"))
	return diffSize{files: files, lines: lines, docOnly: docOnly, ok: true}
}

// diffSize is what pm knows about the change a spawn is being judged against.
// Grouped rather than returned positionally because ok/docOnly are easy to
// transpose at a call site and a silently inverted one would change the review
// regime rather than fail.
type diffSize struct {
	files   int
	lines   int
	docOnly bool
	ok      bool // false when there is no measurable diff; every cap that needs one stands down
}

// cap is how many reviewers this round may spawn.
func (d diffSize) cap() int { return reviewerCap(d.files, d.lines, d.docOnly) }

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

// roundIndex is which review round a spawn happening at `now` belongs to,
// 1-based, counting the rounds already recorded. It reads the same clusters
// AggregateReviewSpawns reports, so the number a worker is refused on is the
// number a retro later sees.
//
// Refused and nested spawns are skipped for the same reason they are skipped in
// spawnsThisRound: a round is a round of REVIEW, and neither of those reviewed
// anything.
func roundIndex(spawns []storage.ReviewSpawn, now time.Time, gap time.Duration) int {
	times := make([]time.Time, 0, len(spawns))
	for _, s := range spawns {
		if s.Nested || s.Denied {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, s.TS); err == nil {
			times = append(times, ts)
		}
	}
	if len(times) == 0 {
		return 1
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	round := 1
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) > gap {
			round++
		}
	}
	// A spawn far enough after the last one opens the next round.
	if now.Sub(times[len(times)-1]) > gap {
		round++
	}
	return round
}

// roundDenyMessage refuses a spawn that would open a round past the project's
// cap. It names the alternative, because the loop this bounds is one a model
// will otherwise keep feeding: an adversarial reviewer asked to refute a change
// almost never returns an empty list, so "repeat until the review is clean" has
// no natural end - measured on epic orbit-106, where all three subs hit
// the cap of 3 and every third round was named some variant of "final
// verification review".
func roundDenyMessage(cap int, docOnly bool) string {
	if docOnly {
		return "pm review cap: this change is documentation only, and pm allows it a single review round. " +
			"A second pass over prose restates the first: act on what the review found, and record anything " +
			"you could not settle in `unresolved` so a human sees it."
	}
	return "pm review cap: this change has already had its " + strconv.Itoa(cap) +
		" review round(s). Another round is not the way to close what is still open: " +
		"fix what the reviews found, and record anything you could not settle in `unresolved` " +
		"so a human sees it. A further confirming review would only restate what you already have."
}

// capDenyMessage is what a refused spawn tells the worker. It names the number
// and where it came from, because a bare refusal sends a model hunting for
// another door - the lesson the hook guard's own deny message was built on.
func capDenyMessage(cap, files, lines int, docOnly bool) string {
	if docOnly {
		return "pm review cap: this change is documentation only (" + strconv.Itoa(files) + " prose file(s), " +
			strconv.Itoa(lines) + " changed line(s)), and pm allows ONE reviewer for a document however long it is - " +
			"length is not risk in prose. The reviewer you already have is the review: it has been told to check the " +
			"document's claims against the repo, which is the part that can actually be wrong. Wait for it, act on what " +
			"it reports, and put anything unsettled in `unresolved`."
	}
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
