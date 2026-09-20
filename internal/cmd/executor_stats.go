package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// Run statuses used in the rollup. done/failed come off the end line (the same
// vocabulary RunState uses); "killed" and "crashed" come off the lines of those
// names, and "crashed" is ALSO synthesised for a start line with no terminal
// partner at all (an unreconciled crash - every journal written before pm
// recorded reasons looks like that). "running" is that same shape while the pid
// is still alive.
const (
	runStatusKilled  = "killed"
	runStatusCrashed = "crashed"
	runStatusRunning = storage.RunStatusRunning
	runStatusUnknown = "unknown"
	// kindUnknown buckets an entry with no `kind`. Spelled separately from
	// runStatusUnknown: one is a run KIND, the other a run STATUS, and they
	// render in adjacent columns.
	kindUnknown = "unknown"
)

// runStatusOrder fixes the print order of run statuses so the output is
// deterministic; anything not listed sorts alphabetically after these.
var runStatusOrder = []string{
	storage.RunStatusDone, storage.RunStatusFailed,
	runStatusKilled, runStatusCrashed, runStatusRunning,
}

// subResultOrder is the full JournalSub.Result vocabulary, in print order. The
// sub histogram prints ALL of these (zeros included) so "0 conflict" is
// visibly different from "conflict is not tracked"; unknown values sort
// alphabetically after them.
// "verified" is what a standalone `pm work` run records (it neither merges nor
// pushes - it opens a draft PR); "merged" and "pushed" are the manager's two
// landing words. All three are green. Journals written before 0.34.0 only ever
// say "merged", which still prints in its own row - they are not rewritten.
var subResultOrder = []string{"verified", "merged", "pushed", "blocked", "failed", "conflict", "skipped", "manual"}

// numStat accumulates a metric plus the number of SAMPLES behind it. Two adders
// because the two levels differ: every end line carries a run duration, so
// run-level stats count every value (addSample), whereas skipped/manual subs
// spawn no worker and report zeros for turns/cost - averaging those in would
// understate the real per-worker figure, so sub-level stats count only non-zero
// values (add). Either way the denominator is printed next to the average.
type numStat struct {
	Total   float64
	Samples int
}

// add counts v only when it is non-zero (sub-level metrics: 0 is
// indistinguishable from "not reported", both being `omitempty` on the wire).
func (n *numStat) add(v float64) {
	n.Total += v
	if v != 0 {
		n.Samples++
	}
}

// addSample counts v unconditionally (run-level metrics, where a 0 is a real
// measurement - e.g. a run that failed within a second).
func (n *numStat) addSample(v float64) {
	n.Total += v
	n.Samples++
}

func (n numStat) avg() float64 {
	if n.Samples == 0 {
		return 0
	}
	return n.Total / float64(n.Samples)
}

// kindStats is the per-kind ("work" / "run-epic") run rollup.
type kindStats struct {
	Runs      int
	Statuses  map[string]int // done | failed | killed | crashed | ...
	DurationS numStat        // run-level wall-clock, off end lines
}

// journalStats is the whole-journal rollup rendered by `pm executor stats`.
type journalStats struct {
	Entries int
	Runs    int // starts seen (one per run)
	Kinds   map[string]*kindStats
	// Sources counts starts by who launched them (JournalEntry.Source):
	// "cockpit" for the web, "" for the CLI and the board - the split the
	// cockpit's run control (pm-cli-118-21) was required to make visible.
	Sources map[string]int
	Crashes int // runs with no orderly end: a reconciled `crashed` line, or a start with no terminal partner at all
	Running int // starts with no terminal partner, holder pid still alive
	// Deaths are the runs that ended by signal or crash AND said why (the
	// `killed`/`crashed` lines carrying an Error). This is the half a count could
	// never give: "crashed 2" tells a retro the shape, the reason tells it what to
	// fix. Ordered as the journal has them, oldest first.
	Deaths      []runDeath
	Subs        int
	SubResults  map[string]int
	SubDuration numStat
	Turns       numStat
	Cost        numStat
	// TokensIn / TokensCacheRead / TokensOut sum the envelope usage of the subs
	// that carry one (TokenSubs is their count, the denominator). Kept apart
	// from Cost because they answer a different question: cost is what the
	// run billed, tokens are what a subscription's rate window METERED - and
	// the cache-read share says which of the two a run was heavy in.
	TokenSubs       int
	TokensIn        int
	TokensCacheRead int
	TokensOut       int
	// EffortSubs / EffortTurns / EffortTokens cover the subs whose worker died
	// before sending a result envelope: they contribute 0 to Turns and Cost
	// because no envelope exists to read those off, which used to make an hour of
	// work indistinguishable from a skip. Reconstructed from the workers' own
	// transcripts (storage.WorkerEffort), kept in their own fields because they
	// are a different measurement - and because their presence is what makes the
	// cost total a floor rather than a number.
	EffortSubs   int
	EffortTurns  int
	EffortTokens int
	// Review-phase telemetry, over the subs that carry it. ReviewSubs is its own
	// denominator on purpose: subs from before the telemetry existed report
	// nothing, and averaging over all subs would quietly dilute the very number
	// a retro is checking.
	ReviewSubs   int
	Spawns       numStat
	Rounds       numStat
	NestedSpawns int
	DiffSpawns   int
	DeniedSpawns int
	// BackgroundSpawns: spawns the worker asked to run in the background (or
	// left to the tool's background default) before pm forced them synchronous.
	BackgroundSpawns int
	ToolCalls        int // exploration calls (Read/Grep/Glob) made by subagents
	ToolDenied       int // of those attempts, how many the per-agent budget refused
	// UnreviewedSubs / UnreviewedCommits: subs that committed AFTER their last
	// reviewer ran, and how many such commits there were in total. This is the
	// structural version of a sentence that used to live only in a worker's prose
	// - "the final mechanism ... landed after the review round cap and has had NO
	// adversarial review" - and it is what separates a `blocked` with findings
	// still open from a `blocked` whose findings were all fixed by a last commit
	// nothing checked.
	UnreviewedSubs    int
	UnreviewedCommits int
	// ReviewSkipped: subs whose worker returned `verified` with zero reviewer
	// spawns on a real change and were demoted to `blocked` by the review
	// floor (pm-cli-130). One such sub landed unnoticed in run pm-cli-129
	// before the floor existed; this line is what makes the next one visible.
	ReviewSkipped int
	ReviewModels  map[string]int // model (or "inherit") -> subs that asked for it
}

// runDeath is one recorded abnormal end: which run, when, and the reason the
// dying manager (or the reconciler that found it) wrote down.
type runDeath struct {
	Event  string // killed | crashed
	TaskID string
	TS     string
	Reason string
}

// runKey identifies a run across its start/terminal lines.
//
// id is the run-instance identifier (JournalEntry.RunID, since 0.27.0) and is
// the only field that identifies a run EXACTLY: with it, two lines share a key
// iff they describe the same physical run. Without it - lines written by an
// older binary - the key degrades to {kind, taskID, pid}, which is ambiguous
// because pids get recycled; kind/taskID/pid are therefore kept in the key even
// when id is set, so the leftover-start pass below can still read a holder pid
// and a kind off the key.
type runKey struct {
	id     string
	kind   string
	taskID string
	pid    int
}

// liveWindow bounds how old an unpaired `start` may be before its live-looking
// pid is treated as pid REUSE rather than a run in flight. The journal is
// append-only and spans months, while macOS recycles its ~100k pid space in
// days - without a bound, one old crash whose pid got reused reads as "running"
// forever and is silently missing from the crash count. Generous enough to
// cover any real run (the executor's own timeout defaults to 120m).
const liveWindow = 24 * time.Hour

// aggregateJournal folds journal entries into the rollup. Start lines are
// paired FIFO with their terminal (end/killed/crashed) line by runKey; leftover
// starts are crashes (or still-running runs, when the pid is alive and recent).
//
// Entries carrying a run id pair EXACTLY (see runKey). The heuristics below
// exist for pre-0.27.0 lines, which have no id and can only be paired by
// {kind, taskID, pid} - a key two different runs can share once a pid is
// recycled.
//
// alive reports whether a pid still belongs to the process that was running at
// the given time, and now is the reference time for liveWindow; both are
// parameters so tests can pin them (production passes storage.ProcessAliveSince
// and time.Now()).
func aggregateJournal(entries []storage.JournalEntry, alive func(int, time.Time) bool, now time.Time) journalStats {
	st := journalStats{
		Entries:    len(entries),
		Kinds:      make(map[string]*kindStats),
		SubResults: make(map[string]int),
		Sources:    make(map[string]int),
	}

	// open[key]     = start lines still awaiting a terminal line.
	// closedBy[key] = the event ("end"/"killed") that closed the last run under
	// this key, so a SECOND terminal line can be classified rather than blindly
	// counted or blindly dropped:
	//   - a DIFFERENT event type means both lines describe ONE physical run -
	//     the kill race. run_epic writes its "end" line (run_epic.go:321) and
	//     then keeps working (gitEnsureBranch, PR creation), during which the
	//     board's kill - gated on a run-state cached up to 2s - can still fire
	//     and append "killed". The reverse order happens too, when a SIGTERM
	//     fails to land and the manager runs to completion anyway.
	//   - the SAME event type twice means two distinct runs that happened to
	//     reuse the pid, the second one's `start` append having been lost.
	// startTS[key] keeps the timestamps of the still-open starts, oldest first.
	open := make(map[runKey]int)
	closedBy := make(map[runKey]string)
	startTS := make(map[runKey][]string)
	// subsAdded[key] records whether the CURRENT run instance under this key
	// already contributed non-empty Subs, so a kill-race duplicate line only
	// folds its payload in when the other terminal line didn't carry any
	// (either order - see the two kill-race cases below). Reset whenever a
	// non-duplicate terminal line starts tracking a fresh run instance (a
	// normal pairing or a reused-key "new run"), since that is a different
	// physical run, not a continuation of the previous one's duplicate state.
	subsAdded := make(map[runKey]bool)

	for _, e := range entries {
		key := runKey{id: e.RunID, kind: e.Kind, taskID: e.TaskID, pid: e.PID}
		switch e.Event {
		case storage.JournalEventStart:
			st.Runs++
			st.kind(e.Kind).Runs++
			st.Sources[e.Source]++
			open[key]++
			startTS[key] = append(startTS[key], e.TS)
		case storage.JournalEventEnd, storage.JournalEventKilled, storage.JournalEventCrashed:
			duplicate := false
			switch {
			case open[key] > 0:
				open[key]--
				if ts := startTS[key]; len(ts) > 0 {
					startTS[key] = ts[1:] // FIFO: the oldest start is the one closed
				}
				closedBy[key] = e.Event
			case closedBy[key] != "" && (key.id != "" || closedBy[key] != e.Event):
				// Second terminal line for a run that is already closed. With a
				// run id the key IS the run, so this is unambiguously the same
				// physical run whatever the event types are - which is what
				// makes a double kill (two "killed" lines, board hit twice
				// before the run-state caught up) count once instead of twice.
				// Without an id, only a DIFFERENT event type can be trusted to
				// mean one run (the end-vs-killed race); the same type twice
				// falls through to the reused-pid case below.
				duplicate = true
			default:
				// Terminal line whose start is missing (a best-effort start
				// append that failed, or a corrupt line ReadJournal skipped).
				// Still a real run, so count it.
				st.Runs++
				st.kind(e.Kind).Runs++
				closedBy[key] = e.Event
			}

			k := st.kind(e.Kind)
			if !duplicate {
				k.Statuses[terminalStatus(e)]++
				// A reconciled crash is counted here rather than by the
				// leftover-start pass below, which can no longer see it: the
				// crashed line closed the start. Both paths feed the same counter,
				// so the total is "runs with no orderly end" either way.
				if e.Event == storage.JournalEventCrashed {
					st.Crashes++
				}
				// Inside the duplicate guard on purpose: a board kill produces
				// TWO killed lines for one physical death (the dying manager's
				// own and killRun's, same run id), and listing both would
				// inflate the deaths report and push genuine crash reasons off
				// its display cap.
				if e.Error != "" && e.Event != storage.JournalEventEnd {
					st.Deaths = append(st.Deaths, runDeath{Event: e.Event, TaskID: e.TaskID, TS: e.TS, Reason: e.Error})
				}
			}
			// Run-level duration is only ever sampled off "end" lines (see
			// kindStats.DurationS) - there is exactly one such line per
			// physical run regardless of duplicate, so no double-count risk.
			if e.Event == storage.JournalEventEnd {
				k.DurationS.addSample(float64(e.DurationS))
			}
			// Sub payload (subs -> turns/cost), in contrast, can now appear on
			// BOTH terminal lines of a kill race ("end" and "killed" each carry
			// Subs). Whichever line closes the run FIRST always folds its subs
			// in (matching pre-fix behaviour when only "end" ever carried
			// them); a second, duplicate line folds its subs in ONLY when the
			// first didn't carry any - covering the reverse race where
			// "killed" closes first with a stale/empty snapshot and the
			// manager's own "end" arrives after with the real payload. Either
			// way, once one line has contributed non-empty subs for this run,
			// the other never does too - so a run never double-counts.
			if duplicate {
				if !subsAdded[key] {
					st.addSubs(e.Subs)
					if len(e.Subs) > 0 {
						subsAdded[key] = true
					}
				}
			} else {
				st.addSubs(e.Subs)
				subsAdded[key] = len(e.Subs) > 0
			}
		}
	}

	// Whatever is still open never got a terminal line. A live holder pid on a
	// RECENT start means the run is in flight right now (stats is routinely run
	// mid-epic); anything else is the documented crash. Same liveness check the
	// TUI uses - including its second criterion, the start line's own stamp, so
	// a pid recycled since that line was written reads as the crash it is. The
	// liveWindow bound stays as the backstop for the platforms that cannot
	// report a process start time.
	for key, n := range open {
		if n == 0 {
			continue // fully paired run - the counter is just a leftover map key
		}
		stamps := startTS[key]
		k := st.kind(key.kind)
		// Classify each open start SEPARATELY. A pid maps to at most one live
		// process, so even when the pid is alive only the NEWEST still-open
		// start can be the run in flight; every older one under the same key is
		// a crash whose pid was later recycled.
		for i := 0; i < n; i++ {
			ts := ""
			if i < len(stamps) {
				ts = stamps[i] // oldest first
			}
			if i == n-1 && recent(ts, now) && aliveSince(alive, key.pid, ts) {
				st.Running++
				k.Statuses[runStatusRunning]++
			} else {
				st.Crashes++
				k.Statuses[runStatusCrashed]++
			}
		}
	}
	return st
}

// aliveSince asks the liveness predicate about a holder pid, handing it the
// start line's own stamp as the reference: the run was demonstrably alive when
// that line was written, so a process now bearing the pid but younger than the
// line is a recycled number and the run is a crash. An unparseable stamp passes
// the zero time, which degrades to bare pid liveness (see ProcessAliveSince).
func aliveSince(alive func(int, time.Time) bool, pid int, stamp string) bool {
	if pid <= 0 || alive == nil {
		return false
	}
	ts, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return alive(pid, time.Time{})
	}
	return alive(pid, ts)
}

// recent reports whether an RFC3339 stamp is within liveWindow of now. A
// missing or unparseable stamp counts as recent: AppendJournal always stamps
// TS, so this only happens on a hand-edited journal, and TS is merely a
// tie-breaker against pid reuse - it should not on its own turn a run whose pid
// IS alive into a crash.
func recent(stamp string, now time.Time) bool {
	ts, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return true
	}
	return now.Sub(ts) < liveWindow
}

// kind returns (creating on demand) the per-kind bucket.
func (s *journalStats) kind(name string) *kindStats {
	if name == "" {
		name = kindUnknown
	}
	k, ok := s.Kinds[name]
	if !ok {
		k = &kindStats{Statuses: make(map[string]int)}
		s.Kinds[name] = k
	}
	return k
}

func (s *journalStats) addSubs(subs []storage.JournalSub) {
	for _, sub := range subs {
		s.Subs++
		res := sub.Result
		if res == "" {
			res = runStatusUnknown
		}
		s.SubResults[res]++
		s.SubDuration.add(float64(sub.DurationS))
		s.Turns.add(float64(sub.Turns))
		s.Cost.add(sub.CostUSD)
		if tk := sub.Tokens; tk != nil {
			s.TokenSubs++
			s.TokensIn += tk.TotalInput()
			s.TokensCacheRead += tk.CacheRead
			s.TokensOut += tk.Output
		}
		if e := sub.Effort; e != nil {
			s.EffortSubs++
			s.EffortTurns += e.Turns
			s.EffortTokens += e.OutputTokens
		}
		if r := sub.Review; r != nil {
			s.ReviewSubs++
			// addSample, not add: a sub that spawned zero reviewers is a real
			// observation and must pull the average DOWN, which is the whole
			// point of measuring. (add() skips zeros, which is right for
			// turns/cost - there a zero means "not reported".)
			s.Spawns.addSample(float64(r.Spawns))
			s.Rounds.addSample(float64(r.Rounds))
			s.NestedSpawns += r.Nested
			s.DiffSpawns += r.WithDiff
			s.DeniedSpawns += r.Denied
			s.BackgroundSpawns += r.Background
			s.ToolCalls += r.ToolCalls
			s.ToolDenied += r.ToolDenied
			if r.UnreviewedCommits > 0 {
				s.UnreviewedSubs++
				s.UnreviewedCommits += r.UnreviewedCommits
			}
			if r.Skipped {
				s.ReviewSkipped++
			}
			if s.ReviewModels == nil {
				s.ReviewModels = map[string]int{}
			}
			models := r.Models
			if models == "" {
				models = "unknown"
			}
			s.ReviewModels[models]++
		}
	}
}

// gateResults are the sub outcomes recorded WITHOUT spawning a worker - the
// manager's gate decisions (classifySub). They matter because `pm run-epic` is
// re-entrant: every re-run re-records already-done, not-ready and dep-blocked
// subs as "skipped" and human-gated ones as "manual", so on a re-run these
// dominate the histogram while representing no actual work.
var gateResults = map[string]bool{"skipped": true, "manual": true}

// workerBacked splits the sub count into subs a worker actually ran and gate
// decisions the manager recorded without spawning one.
func (s journalStats) workerBacked() (worked, gated int) {
	for res, n := range s.SubResults {
		if gateResults[res] {
			gated += n
		} else {
			worked += n
		}
	}
	return worked, gated
}

// terminalStatus maps a terminal entry to its run status.
func terminalStatus(e storage.JournalEntry) string {
	if e.Event == storage.JournalEventKilled {
		return runStatusKilled
	}
	// A reconciled crash carries Status "failed" (the run did fail), but as a run
	// STATUS it is a crash - the histogram must not report it as an orderly
	// failure, and a crash reconciled from a run-state has to land in the same row
	// as one still detectable only as an orphaned start.
	if e.Event == storage.JournalEventCrashed {
		return runStatusCrashed
	}
	if e.Status == "" {
		return runStatusUnknown
	}
	return e.Status
}

// orderedKeys returns the keys of counts sorted by the fixed `order` first,
// then anything else alphabetically - so output never depends on map order.
// keepZero controls whether `order` entries with a zero count are still
// emitted: the sub histogram wants them (a visible "0 conflict" beats a missing
// row), the run-status line does not (a bare "0 crashed" is noise).
// Keys outside `order` are always dropped when zero - they only exist because
// something wrote them.
func orderedKeys(counts map[string]int, order []string, keepZero bool) []string {
	seen := make(map[string]bool, len(order))
	out := make([]string, 0, len(counts)+len(order))
	for _, k := range order {
		seen[k] = true
		if counts[k] > 0 || keepZero {
			out = append(out, k)
		}
	}
	var rest []string
	for k, v := range counts {
		if !seen[k] && v > 0 {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// renderJournalStats formats the rollup for a terminal.
func renderJournalStats(slug, path string, st journalStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# executor stats: %s\n", slug)
	// "parsed": ReadJournal skips unparseable lines, so this can be lower than
	// the file's line count.
	fmt.Fprintf(&b, "# %s (%d parsed entries)\n\n", path, st.Entries)

	// The status is how the MANAGER exited, not a verdict on the work: run-epic
	// hardcodes "done" on its end line whatever the subs did (run_epic.go), so
	// "done" means "the sub loop returned", and the outcome signal lives in the
	// sub histogram below. Saying so beats letting a retro read "2 done" as two
	// successful runs.
	fmt.Fprintf(&b, "## Runs: %d  (status = how the manager exited, not whether the work landed)\n", st.Runs)
	kinds := make([]string, 0, len(st.Kinds))
	for k := range st.Kinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, name := range kinds {
		k := st.Kinds[name]
		fmt.Fprintf(&b, "  %-9s %d run(s)", name, k.Runs)
		var parts []string
		for _, s := range orderedKeys(k.Statuses, runStatusOrder, false) {
			parts = append(parts, fmt.Sprintf("%d %s", k.Statuses[s], s))
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "  [%s]", strings.Join(parts, ", "))
		}
		fmt.Fprintln(&b)
		if k.DurationS.Samples > 0 {
			// Denominator is ended runs, not k.Runs: killed/crashed/running
			// runs have no end line and so no recorded duration.
			fmt.Fprintf(&b, "            duration %s total, %s avg (%d ended run(s))\n",
				fmtDuration(k.DurationS.Total), fmtDuration(k.DurationS.avg()), k.DurationS.Samples)
		}
	}
	// Totals across all kinds - the per-kind brackets above already count these,
	// so they are restated here only because they are the two states with no
	// terminal line at all, which is what the "detected crashes" ask is about.
	if st.Crashes > 0 || st.Running > 0 {
		fmt.Fprintln(&b, "  -- across all kinds (already counted above):")
		if st.Crashes > 0 {
			fmt.Fprintf(&b, "     crashed %d (a reconciled crash line, or a start with no terminal line at all)\n", st.Crashes)
		}
		if st.Running > 0 {
			fmt.Fprintf(&b, "     running %d (no terminal line yet, pid still alive)\n", st.Running)
		}
	}
	if n := len(st.Sources); n > 1 || (n == 1 && st.Sources[""] == 0) {
		// Only worth a line once something other than the CLI/board has
		// launched a run; "" is those two (they set no source).
		parts := make([]string, 0, n)
		for _, src := range orderedKeys(st.Sources, nil, true) {
			label := src
			if label == "" {
				label = "cli/board"
			}
			parts = append(parts, fmt.Sprintf("%d %s", st.Sources[src], label))
		}
		fmt.Fprintf(&b, "  launched from: %s\n", strings.Join(parts, ", "))
	}
	renderDeaths(&b, st.Deaths)

	// Re-entrancy warning is not cosmetic: a re-run of an 8-sub epic where 5
	// subs are already done records 5 more "skipped" rows, so the raw count
	// drifts far from the work actually done.
	worked, gated := st.workerBacked()
	fmt.Fprintf(&b, "\n## Subs: %d  (%d worker-backed, %d gate decision(s) - re-runs re-record skips)\n",
		st.Subs, worked, gated)
	for _, r := range orderedKeys(st.SubResults, subResultOrder, true) {
		label := ""
		if gateResults[r] {
			label = "  (gate)"
		}
		fmt.Fprintf(&b, "  %-9s %d%s\n", r, st.SubResults[r], label)
	}

	// Printed unconditionally: a journal of only killed/crashed runs carries no
	// subs, and "0 total" is the answer to "how much did this cost", not a
	// reason to omit the section. Each line prints its own denominator because
	// the three differ - a sub can report a duration but no turns/cost.
	fmt.Fprintf(&b, "\n## Totals (each averaged over the subs that reported that value)\n")
	fmt.Fprintf(&b, "  duration  %s total, %s avg (%d sub(s))\n",
		fmtDuration(st.SubDuration.Total), fmtDuration(st.SubDuration.avg()), st.SubDuration.Samples)
	fmt.Fprintf(&b, "  turns     %.0f total, %.1f avg (%d sub(s))\n",
		st.Turns.Total, st.Turns.avg(), st.Turns.Samples)
	fmt.Fprintf(&b, "  cost      $%.2f total, $%.2f avg (%d sub(s))\n",
		st.Cost.Total, st.Cost.avg(), st.Cost.Samples)
	if st.TokenSubs > 0 {
		// Tokens READ is the rate-limit figure; the cache share is what tells a
		// long run from an expensive one (retro pm-cli-119: 95-96% of ~49M).
		share := 0.0
		if st.TokensIn > 0 {
			share = 100 * float64(st.TokensCacheRead) / float64(st.TokensIn)
		}
		fmt.Fprintf(&b, "  tokens    %s read total (%.0f%% cache reads), %s avg per sub; %s written (%d sub(s) carried usage)\n",
			fmtTokens(st.TokensIn), share, fmtTokens(st.TokensIn/st.TokenSubs), fmtTokens(st.TokensOut), st.TokenSubs)
	}
	if st.EffortSubs > 0 {
		// The line that stops the cost total being read as a total. These subs
		// died before their envelope, so they are worth $0.00 and 0 turns above
		// however long they ran; what their transcripts hold is printed instead,
		// with no dollar figure derived from it (a transcript carries tokens, not
		// prices).
		fmt.Fprintf(&b, "  partial   %d sub(s) died with no envelope, so they count as 0 turns / $0 above - their transcripts hold %d turn(s) and %d output token(s), which makes the cost total a FLOOR\n",
			st.EffortSubs, st.EffortTurns, st.EffortTokens)
	}
	renderReviewStats(&b, st)
	return b.String()
}

// deathReasonsShown bounds the printed reasons. The journal spans months, and a
// retro reads this to ask "what killed the recent runs" - an unbounded list would
// push the sub histogram off the screen. The count line says how many were held
// back, and the journal itself holds all of them.
const deathReasonsShown = 5

// fmtTokens prints a token count the way people say it: 48.7M, 266k, 981.
func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// renderDeaths prints why the abnormal ends happened, newest last. Nothing is
// printed when nothing said why - which is what every journal line written before
// pm recorded reasons looks like, and reporting "0 reasons" on those would read
// as "the deaths had no cause" rather than "nobody was writing them down".
func renderDeaths(b *strings.Builder, deaths []runDeath) {
	if len(deaths) == 0 {
		return
	}
	shown := deaths
	if len(shown) > deathReasonsShown {
		shown = shown[len(shown)-deathReasonsShown:]
		fmt.Fprintf(b, "  -- why runs ended abnormally (last %d of %d recorded):\n", len(shown), len(deaths))
	} else {
		fmt.Fprintf(b, "  -- why runs ended abnormally (%d recorded):\n", len(deaths))
	}
	for _, d := range shown {
		fmt.Fprintf(b, "     %s %s %s\n        %s\n", d.TS, d.Event, d.TaskID, d.Reason)
	}
}

// renderReviewStats prints the review-phase section. Omitted entirely when no
// sub carries telemetry - on a journal written before it existed there is
// nothing to say, and a block of zeros would read as "the review phase did
// nothing" rather than "this was not measured".
func renderReviewStats(b *strings.Builder, st journalStats) {
	if st.ReviewSubs == 0 {
		return
	}
	worked, _ := st.workerBacked()
	fmt.Fprintf(b, "\n## Review phase (%d of %d worker-backed sub(s) measured)\n", st.ReviewSubs, worked)
	fmt.Fprintf(b, "  spawns    %.0f total, %.1f avg per sub\n", st.Spawns.Total, st.Spawns.avg())
	fmt.Fprintf(b, "  rounds    %.0f total, %.1f avg per sub\n", st.Rounds.Total, st.Rounds.avg())
	if st.Spawns.Total > 0 {
		// Both this and the model line below report what the WORKER asked for,
		// not what pm delivered - pm attaches the diff to the rest and pins the
		// model on all of them. Reporting pm's own substitutions would make this
		// agree with pm by construction; what is worth measuring is how far the
		// prompt rules are followed on their own.
		fmt.Fprintf(b, "  with diff %d of %.0f spawn(s) arrived carrying a diff (pm attached one to the rest)\n",
			st.DiffSpawns, st.Spawns.Total)
	}
	if st.DeniedSpawns > 0 {
		// The only visible evidence that the cap did anything. Without it an
		// enforced run and a well-behaved one look identical in the rollup.
		fmt.Fprintf(b, "  refused   %d spawn(s) refused by the cap (these never ran)\n", st.DeniedSpawns)
	}
	if st.NestedSpawns > 0 {
		// Called out rather than folded into the total: these are spawned from
		// inside another subagent, so a cap that only watches the worker never
		// sees them (measured once: one reviewer's own Explore subagent
		// burned 6.1M tokens over 65 tool calls).
		fmt.Fprintf(b, "  nested    %d spawn(s) issued from inside another subagent\n", st.NestedSpawns)
	}
	if st.BackgroundSpawns > 0 {
		// What the worker asked for - pm forced these synchronous (pm-cli-105),
		// so the count measures the ask, not a review that was actually lost.
		fmt.Fprintf(b, "  async ask %d spawn(s) asked for (or defaulted to) background; pm forced them synchronous\n", st.BackgroundSpawns)
	}
	if st.UnreviewedSubs > 0 {
		// The line a retro is looking for when a sub came back `blocked` after a
		// long review loop: not "was something unresolved" but "how much of this
		// branch did no reviewer see". A capped loop cannot review its own last
		// fix, so this is expected to be non-zero sometimes - what matters is that
		// it is a number on the branch, not a sentence in a note.
		fmt.Fprintf(b, "  unreviewed %d sub(s) landed %d commit(s) after their last review - nothing reviewed those\n",
			st.UnreviewedSubs, st.UnreviewedCommits)
	}
	if st.ReviewSkipped > 0 {
		// The floor's evidence line: a worker tried to land a change no
		// reviewer saw, and pm parked it instead of merging it.
		fmt.Fprintf(b, "  review skipped: %d sub(s) returned verified with 0 reviewer spawns - demoted to blocked by the review floor\n", st.ReviewSkipped)
	}
	if st.ToolCalls > 0 || st.ToolDenied > 0 {
		// The budget's own evidence line: without the refusal count an enforced
		// run and a well-behaved one look identical in the rollup.
		fmt.Fprintf(b, "  agent I/O %d exploration call(s) by subagents, %d refused by the per-agent budget\n",
			st.ToolCalls, st.ToolDenied)
	}
	for _, m := range orderedKeys(st.ReviewModels, nil, false) {
		label := m
		if m == "inherit" {
			label = "inherit (pm pinned)"
		}
		fmt.Fprintf(b, "  model     %-20s %d sub(s)  (asked for by the worker)\n", label, st.ReviewModels[m])
	}
}

// fmtDuration renders seconds as a compact human duration (e.g. "1h04m", "7m12s").
func fmtDuration(secs float64) string {
	s := int(secs + 0.5)
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	case s >= 60:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func newExecutorStatsCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "stats [project]",
		Short: "Summarise the executor journal: runs, outcomes, crashes, turns, cost",
		Long: "Reads <project>/.executor/journal.jsonl (the append-only executor history) and prints a " +
			"rollup: runs per kind (work/run-epic) and terminal status, detected crashes (a start line " +
			"with no end/killed partner), the sub outcome histogram, and duration/turns/cost totals + " +
			"averages. Read-only; an empty or missing journal prints a note, not an error.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := resolveProjectSlugArg(store, args)
			if err != nil {
				return err
			}

			dir := store.ProjectDir(slug)
			path := storage.JournalPath(dir)
			entries, err := storage.ReadJournal(dir)
			if err != nil {
				// ReadJournal returns what it parsed ALONGSIDE the error (a
				// line over its 1MB scanner cap, an I/O fault mid-file). Its
				// contract is that one bad line never hides the rest of the
				// history, so degrade to a partial rollup + a warning rather
				// than failing the command outright.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: journal read incomplete (%v) - rollup covers %d entry(ies)\n",
					err, len(entries))
			}
			if len(entries) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no executor runs recorded yet for %s (%s)\n", slug, path)
				return nil
			}
			stats := aggregateJournal(entries, storage.ProcessAliveSince, time.Now())
			fmt.Fprint(cmd.OutOrStdout(), renderJournalStats(slug, path, stats))
			return nil
		},
	}
}
