package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// Run statuses used in the rollup. done/failed come off the end line (the same
// vocabulary RunState uses); the rest are synthesised here for a start line
// with no terminal partner - "crashed" is the documented start-without-end
// convention, "running" is that same shape while the pid is still alive.
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
var subResultOrder = []string{"merged", "blocked", "failed", "conflict", "skipped", "manual"}

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
	Entries     int
	Runs        int // starts seen (one per run)
	Kinds       map[string]*kindStats
	Crashes     int // starts with no end/killed partner, holder pid dead
	Running     int // starts with no end/killed partner, holder pid still alive
	Subs        int
	SubResults  map[string]int
	SubDuration numStat
	Turns       numStat
	Cost        numStat
}

// runKey identifies a run across its start/terminal lines. The PID
// discriminates repeated runs of the same task, and the board's killed line
// carries the manager's PID + kind, so it pairs with the right start.
type runKey struct {
	kind   string
	taskID string
	pid    int
}

// aggregateJournal folds journal entries into the rollup. Start lines are
// paired FIFO with their terminal (end/killed) line by runKey; leftover starts
// are crashes (or still-running runs, when the pid is alive).
//
// alive reports whether a pid still belongs to a live process; it is a
// parameter so tests can pin it (production passes storage.ProcessAlive).
func aggregateJournal(entries []storage.JournalEntry, alive func(int) bool) journalStats {
	st := journalStats{
		Entries:    len(entries),
		Kinds:      make(map[string]*kindStats),
		SubResults: make(map[string]int),
	}

	// open[key]   = start lines still awaiting a terminal line.
	// closed[key] = runs already paired with a terminal line, so a SECOND
	// terminal line for the same key can be recognised as a duplicate rather
	// than invented as a new run. That happens for real: run_epic writes its
	// "end" line and then keeps working (PR creation), during which the board
	// can still fire a kill and append a "killed" line for the same run.
	open := make(map[runKey]int)
	closed := make(map[runKey]int)

	for _, e := range entries {
		key := runKey{kind: e.Kind, taskID: e.TaskID, pid: e.PID}
		switch e.Event {
		case storage.JournalEventStart:
			st.Runs++
			st.kind(e.Kind).Runs++
			open[key]++
		case storage.JournalEventEnd, storage.JournalEventKilled:
			switch {
			case open[key] > 0:
				open[key]--
				closed[key]++
			case closed[key] > 0:
				// Duplicate terminal line for an already-closed run (the kill
				// race above). The first terminal line carries the real
				// duration and subs - ignore this one entirely rather than
				// double-count the run.
				continue
			default:
				// Terminal line whose start is missing (a best-effort start
				// append that failed, or a corrupt line ReadJournal skipped).
				// Still a real run, so count it.
				st.Runs++
				st.kind(e.Kind).Runs++
				closed[key]++
			}
			k := st.kind(e.Kind)
			k.Statuses[terminalStatus(e)]++
			if e.Event == storage.JournalEventEnd {
				k.DurationS.addSample(float64(e.DurationS))
			}
			st.addSubs(e.Subs)
		}
	}

	// Whatever is still open never got a terminal line. A live holder pid means
	// the run is in flight right now (stats is routinely run mid-epic); a dead
	// one is the documented crash. Same signal-0 liveness check the TUI uses,
	// so it inherits the same pid-reuse caveat.
	for key, n := range open {
		if n == 0 {
			continue // fully paired run - the counter is just a leftover map key
		}
		status := runStatusCrashed
		if key.pid > 0 && alive != nil && alive(key.pid) {
			status = runStatusRunning
			st.Running += n
		} else {
			st.Crashes += n
		}
		st.kind(key.kind).Statuses[status] += n
	}
	return st
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
	}
}

// terminalStatus maps a terminal entry to its run status.
func terminalStatus(e storage.JournalEntry) string {
	if e.Event == storage.JournalEventKilled {
		return runStatusKilled
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

	fmt.Fprintf(&b, "## Runs: %d\n", st.Runs)
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
	if st.Crashes > 0 {
		fmt.Fprintf(&b, "  crashed:  %d (start with no end/killed line)\n", st.Crashes)
	}
	if st.Running > 0 {
		fmt.Fprintf(&b, "  running:  %d (start with no end/killed line, pid still alive)\n", st.Running)
	}

	fmt.Fprintf(&b, "\n## Subs: %d\n", st.Subs)
	for _, r := range orderedKeys(st.SubResults, subResultOrder, true) {
		fmt.Fprintf(&b, "  %-9s %d\n", r, st.SubResults[r])
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
	return b.String()
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
			var slug string
			if len(args) == 1 {
				s, err := store.ResolveProject(args[0])
				if err != nil {
					return err
				}
				slug = s
			} else {
				slug = detectProjectFromCwd(store)
			}
			if slug == "" {
				return fmt.Errorf("no project (pass a project or run inside a project dir)")
			}

			dir := store.ProjectDir(slug)
			path := storage.JournalPath(dir)
			entries, err := storage.ReadJournal(dir)
			if err != nil {
				return fmt.Errorf("read journal: %w", err)
			}
			if len(entries) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "no executor runs recorded yet for %s (%s)\n", slug, path)
				return nil
			}
			stats := aggregateJournal(entries, storage.ProcessAlive)
			fmt.Fprint(cmd.OutOrStdout(), renderJournalStats(slug, path, stats))
			return nil
		},
	}
}
