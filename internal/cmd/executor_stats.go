package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// Terminal run statuses used in the rollup. "killed" comes from the killed
// event, "crashed" is synthesised for a start line with no terminal partner
// (the documented crash convention of the journal).
const (
	runStatusKilled  = "killed"
	runStatusCrashed = "crashed"
	runStatusUnknown = "unknown"
)

// runStatusOrder fixes the print order of run statuses so the output is
// deterministic; anything not listed sorts alphabetically after these.
var runStatusOrder = []string{"done", "failed", runStatusKilled, runStatusCrashed}

// subResultOrder fixes the print order of sub outcomes (the JournalSub.Result
// vocabulary); unknown values sort alphabetically after these.
var subResultOrder = []string{"merged", "blocked", "failed", "conflict", "skipped", "manual"}

// numStat accumulates a metric plus the number of SAMPLES that carried a
// non-zero value. Skipped/manual subs spawn no worker and report zeros for
// turns/cost, so averaging over every sub would understate the real per-worker
// figure - the average is over Samples, and the denominator is printed.
type numStat struct {
	Total   float64
	Samples int
}

func (n *numStat) add(v float64) {
	n.Total += v
	if v != 0 {
		n.Samples++
	}
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
	Crashes     int // starts with no end/killed partner
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
// are crashes.
func aggregateJournal(entries []storage.JournalEntry) journalStats {
	st := journalStats{
		Entries:    len(entries),
		Kinds:      make(map[string]*kindStats),
		SubResults: make(map[string]int),
	}

	// open[key] = count of start lines still awaiting a terminal line.
	open := make(map[runKey]int)

	for _, e := range entries {
		key := runKey{kind: e.Kind, taskID: e.TaskID, pid: e.PID}
		switch e.Event {
		case storage.JournalEventStart:
			st.Runs++
			st.kind(e.Kind).Runs++
			open[key]++
		case storage.JournalEventEnd, storage.JournalEventKilled:
			if open[key] > 0 {
				open[key]--
			} else {
				// Terminal line with no start in this journal (truncated
				// history) - still a real run, count it.
				st.Runs++
				st.kind(e.Kind).Runs++
			}
			k := st.kind(e.Kind)
			k.Statuses[terminalStatus(e)]++
			if e.Event == storage.JournalEventEnd {
				k.DurationS.add(float64(e.DurationS))
			}
			st.addSubs(e.Subs)
		}
	}

	for key, n := range open {
		if n <= 0 {
			continue // fully paired run - the counter is just a leftover map key
		}
		st.Crashes += n
		st.kind(key.kind).Statuses[runStatusCrashed] += n
	}
	return st
}

// kind returns (creating on demand) the per-kind bucket.
func (s *journalStats) kind(name string) *kindStats {
	if name == "" {
		name = runStatusUnknown
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
func orderedKeys(counts map[string]int, order []string) []string {
	seen := make(map[string]bool, len(order))
	out := make([]string, 0, len(counts))
	for _, k := range order {
		seen[k] = true
		if counts[k] > 0 {
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
	fmt.Fprintf(&b, "# %s (%d entries)\n\n", path, st.Entries)

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
		for _, s := range orderedKeys(k.Statuses, runStatusOrder) {
			parts = append(parts, fmt.Sprintf("%d %s", k.Statuses[s], s))
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "  [%s]", strings.Join(parts, ", "))
		}
		fmt.Fprintln(&b)
		if k.DurationS.Samples > 0 {
			fmt.Fprintf(&b, "            duration %s total, %s avg (%d run(s))\n",
				fmtDuration(k.DurationS.Total), fmtDuration(k.DurationS.avg()), k.DurationS.Samples)
		}
	}
	if st.Crashes > 0 {
		fmt.Fprintf(&b, "  crashed:  %d (start with no end/killed line)\n", st.Crashes)
	}

	fmt.Fprintf(&b, "\n## Subs: %d\n", st.Subs)
	if st.Subs > 0 {
		for _, r := range orderedKeys(st.SubResults, subResultOrder) {
			fmt.Fprintf(&b, "  %-9s %d\n", r, st.SubResults[r])
		}
		fmt.Fprintf(&b, "\n## Totals (averaged over subs that reported a value)\n")
		fmt.Fprintf(&b, "  duration  %s total, %s avg (%d sub(s))\n",
			fmtDuration(st.SubDuration.Total), fmtDuration(st.SubDuration.avg()), st.SubDuration.Samples)
		fmt.Fprintf(&b, "  turns     %.0f total, %.1f avg (%d sub(s))\n",
			st.Turns.Total, st.Turns.avg(), st.Turns.Samples)
		fmt.Fprintf(&b, "  cost      $%.2f total, $%.2f avg (%d sub(s))\n",
			st.Cost.Total, st.Cost.avg(), st.Cost.Samples)
	}
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
			fmt.Fprint(cmd.OutOrStdout(), renderJournalStats(slug, path, aggregateJournal(entries)))
			return nil
		},
	}
}
