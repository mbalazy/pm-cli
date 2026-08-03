package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// `pm journal` is the read/write surface of the subsystem journal (see
// internal/storage/journal.go): a per-project running record of how one chosen,
// repeatedly-troublesome subsystem actually behaves.
//
// The subcommands take the journal NAME as their argument and the project as a
// --project flag rather than as a second positional: `pm journal show sim-rig
// app-orbit` reads as two names in no obvious order, and the project is
// usually settled by cwd anyway.

func newJournalCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "journal",
		Short: "Running record of a repeatedly-troublesome subsystem (per project)",
		Long: "A journal tracks how ONE chosen subsystem actually behaves in a project - what bit you, when, " +
			"what it looked like, what you wrongly concluded, the real cause, and the flow/tool fix it argues for.\n\n" +
			"An entry is an EVENT: dated, append-only, never rewritten. That is what separates it from a doc or a " +
			"memory file, which holds a RULE and gets rewritten when the rule turns out wrong - and it is why a " +
			"journal can answer \"fifth time this month\" while a rule never can. An entry with no fix recorded is " +
			"OPEN, and the open set is the backlog.\n\n" +
			"Which subsystems a project journals is declared in project.yaml:\n\n" +
			"    journals:\n" +
			"        - name: sim-rig\n" +
			"          subject: iOS simulator rig (which sim, which Metro, which build)\n",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJournalList(cmd, store)
		},
	}
	cmd.PersistentFlags().StringP("project", "p", "", "project slug (default: detected from cwd)")
	cmd.AddCommand(
		newJournalListCmd(store),
		newJournalShowCmd(store),
		newJournalAddCmd(store),
		newJournalStatsCmd(store),
	)
	return cmd
}

// journalProject resolves the project from --project, else the cwd, and loads
// it - every journal subcommand needs the project struct, because the declared
// `journals:` list is the only thing that says which journals exist.
func journalProject(cmd *cobra.Command, store storage.TaskStore) (string, *storage.Project, error) {
	flag, _ := cmd.Flags().GetString("project")
	var args []string
	if flag != "" {
		args = []string{flag}
	}
	return resolveProjectArg(store, args)
}

// journalSubject resolves a journal name against the project's declared list.
// Undeclared is an ERROR, not an implicit create: a journal is a deliberate
// decision about what is worth tracking, and a typo that silently opens a
// second file would split a history in half without saying so.
func journalSubject(proj *storage.Project, name string) (*storage.JournalSubject, error) {
	if s := proj.Journal(name); s != nil {
		return s, nil
	}
	declared := proj.JournalNames()
	if len(declared) == 0 {
		return nil, fmt.Errorf("no journals declared for this project - add a `journals:` list to project.yaml:\n"+
			"    journals:\n        - name: %s\n          subject: <what is being tracked>", name)
	}
	return nil, fmt.Errorf("no journal %q in this project (declared: %s)", name, strings.Join(declared, ", "))
}

func newJournalListCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the journals declared for a project, with entry counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runJournalList(cmd, store)
		},
	}
}

func runJournalList(cmd *cobra.Command, store storage.TaskStore) error {
	slug, proj, err := journalProject(cmd, store)
	if err != nil {
		return err
	}
	counts, err := storage.JournalCounts(store.ProjectDir(slug), proj)
	if err != nil {
		return err
	}
	if len(counts) == 0 {
		fmt.Printf("no journals declared for %s\n\n", slug)
		fmt.Print("Declare one in project.yaml to start tracking a subsystem that keeps costing you time:\n\n" +
			"    journals:\n        - name: sim-rig\n          subject: iOS simulator rig\n")
		return nil
	}
	fmt.Printf("# journals: %s\n\n", slug)
	for _, c := range counts {
		open := ""
		if c.Open > 0 {
			open = fmt.Sprintf(", %d open", c.Open)
		}
		fmt.Printf("  %-20s %d entr%s%s\n", c.Name, c.Total, plural(c.Total, "y", "ies"), open)
		if c.Subject != "" {
			fmt.Printf("  %-20s %s\n", "", c.Subject)
		}
	}
	fmt.Printf("\n  pm journal show <name>   pm journal stats <name>\n")
	return nil
}

// journalPointer renders the one-line summary that `pm context` and
// pm_context print instead of any journal content: "sim-rig 12 (3 open)".
// Open counts lead the reader to `pm journal stats`, which is where the
// backlog actually is.
func journalPointer(counts []storage.JournalCount) string {
	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		s := fmt.Sprintf("%s %d", c.Name, c.Total)
		if c.Open > 0 {
			s += fmt.Sprintf(" (%d open)", c.Open)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ") + "  ->  pm journal show <name>"
}

func newJournalShowCmd(store storage.TaskStore) *cobra.Command {
	var limit int
	var openOnly bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Print a journal's entries, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, proj, err := journalProject(cmd, store)
			if err != nil {
				return err
			}
			subject, err := journalSubject(proj, args[0])
			if err != nil {
				return err
			}
			incidents, err := storage.ReadIncidents(store.ProjectDir(slug), subject.Name)
			if err != nil {
				return err
			}
			fmt.Print(renderJournalEntries(slug, subject, incidents, limit, openOnly))
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "max entries to print (0 = all)")
	cmd.Flags().BoolVar(&openOnly, "open", false, "only entries with no fix recorded")
	return cmd
}

// renderJournalEntries prints newest first - the opposite of the file order.
// The file is append-only history; the reader almost always wants "what has
// been happening lately", and a reader who wants the beginning passes --limit 0.
func renderJournalEntries(slug string, subject *storage.JournalSubject, incidents []storage.Incident, limit int, openOnly bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s / %s\n", slug, subject.Name)
	if subject.Subject != "" {
		fmt.Fprintf(&b, "%s\n", subject.Subject)
	}
	b.WriteString("\n")

	shown := make([]storage.Incident, 0, len(incidents))
	for _, in := range incidents {
		if openOnly && !in.Open() {
			continue
		}
		shown = append(shown, in)
	}
	// Newest first, undated last (same rule as the open list in stats).
	sort.SliceStable(shown, func(i, j int) bool {
		a, aOK := shown[i].When()
		bb, bOK := shown[j].When()
		if aOK != bOK {
			return aOK
		}
		if !aOK {
			return false
		}
		return a.After(bb)
	})

	total := len(shown)
	if total == 0 {
		if openOnly {
			b.WriteString("no open entries - every recorded incident names the fix it produced.\n")
		} else {
			b.WriteString("no entries yet.\n")
		}
		return b.String()
	}
	if limit > 0 && total > limit {
		shown = shown[:limit]
	}

	for _, in := range shown {
		fmt.Fprintf(&b, "%s  %s\n", journalDate(in), in.Symptom)
		if in.FalseConclusion != "" {
			fmt.Fprintf(&b, "    concluded (wrongly): %s\n", in.FalseConclusion)
		}
		if in.Cause != "" {
			fmt.Fprintf(&b, "    cause:               %s\n", in.Cause)
		}
		if in.Fix != "" {
			fmt.Fprintf(&b, "    fix:                 %s\n", in.Fix)
		} else {
			fmt.Fprintf(&b, "    fix:                 OPEN\n")
		}
		var meta []string
		if in.CostMin > 0 {
			meta = append(meta, fmt.Sprintf("%d min", in.CostMin))
		}
		if len(in.Tags) > 0 {
			meta = append(meta, strings.Join(in.Tags, " "))
		}
		if len(meta) > 0 {
			fmt.Fprintf(&b, "    %s\n", strings.Join(meta, "  |  "))
		}
		b.WriteString("\n")
	}
	if len(shown) < total {
		fmt.Fprintf(&b, "%d of %d shown - raise --limit (0 = all).\n", len(shown), total)
	}
	return b.String()
}

// journalDate renders the day only. The time of day has never once mattered
// for reading these back; the date is what carries recurrence.
func journalDate(in storage.Incident) string {
	if t, ok := in.When(); ok {
		return t.Local().Format("2006-01-02")
	}
	return "??????????"
}

func newJournalAddCmd(store storage.TaskStore) *cobra.Command {
	var in storage.Incident
	var when string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Append an entry to a journal",
		Long: "Appends one incident. --symptom is required: it is what a future reader will recognise the " +
			"situation by, before the cause is known.\n\n" +
			"Leave --fix empty while the fix is not made yet - an entry with no fix is OPEN, and the open set " +
			"is what `pm journal stats` turns into a backlog. Do not fill it with \"n/a\".",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, proj, err := journalProject(cmd, store)
			if err != nil {
				return err
			}
			subject, err := journalSubject(proj, args[0])
			if err != nil {
				return err
			}
			if when != "" {
				t, perr := time.Parse("2006-01-02", when)
				if perr != nil {
					// Seeding history is a first-class use (that is how a
					// journal starts with the incidents that argued for it),
					// so say exactly what shape is expected.
					return fmt.Errorf("--date %q: want YYYY-MM-DD", when)
				}
				in.TS = t.Format(time.RFC3339)
			}
			if err := storage.AppendIncident(store.ProjectDir(slug), subject.Name, &in); err != nil {
				return err
			}
			state := "OPEN"
			if !in.Open() {
				state = "fix recorded"
			}
			fmt.Printf("%s / %s: entry added (%s, %s)\n", slug, subject.Name, journalDate(in), state)
			return nil
		},
	}
	cmd.Flags().StringVar(&in.Symptom, "symptom", "", "what it looked like before you knew the cause (required)")
	cmd.Flags().StringVar(&in.FalseConclusion, "false", "", "the wrong belief the symptom produced")
	cmd.Flags().StringVar(&in.Cause, "cause", "", "what was actually going on")
	cmd.Flags().IntVar(&in.CostMin, "cost", 0, "minutes lost")
	cmd.Flags().StringVar(&in.Fix, "fix", "", "the flow/tool change it argues for, and where it landed (empty = open)")
	cmd.Flags().StringSliceVar(&in.Tags, "tag", nil, "tag grouping incidents that share a cause (repeatable)")
	cmd.Flags().StringVar(&in.Session, "session", "", "Claude session id")
	cmd.Flags().StringVar(&when, "date", "", "back-date the entry (YYYY-MM-DD) when seeding history")
	return cmd
}

func newJournalStatsCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "stats <name>",
		Short: "Recurrence, cost and the open backlog for a journal",
		Long: "Turns a pile of entries into the argument for a fix: how often, how much time, which causes " +
			"cluster, and what is still open. The open list is DERIVED from entries with no fix recorded - " +
			"there is no separate backlog to maintain.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, proj, err := journalProject(cmd, store)
			if err != nil {
				return err
			}
			subject, err := journalSubject(proj, args[0])
			if err != nil {
				return err
			}
			incidents, err := storage.ReadIncidents(store.ProjectDir(slug), subject.Name)
			if err != nil {
				return err
			}
			fmt.Print(renderIncidentStats(slug, subject, storage.AggregateIncidents(incidents)))
			return nil
		},
	}
}

func renderIncidentStats(slug string, subject *storage.JournalSubject, st storage.JournalStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# journal stats: %s / %s\n", slug, subject.Name)
	if subject.Subject != "" {
		fmt.Fprintf(&b, "%s\n", subject.Subject)
	}
	b.WriteString("\n")

	if st.Total == 0 {
		b.WriteString("no entries yet.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "entries: %d", st.Total)
	if st.First != "" {
		fmt.Fprintf(&b, "  (%s .. %s)", shortDate(st.First), shortDate(st.Last))
	}
	if st.Undated > 0 {
		fmt.Fprintf(&b, "  [%d undated]", st.Undated)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "open:    %d\n", st.Open)
	if st.CostMin > 0 {
		// The average's denominator is the entries that RECORDED a cost -
		// dividing by st.Total would report a cheaper subsystem the more
		// often someone skipped the field.
		fmt.Fprintf(&b, "cost:    %s over %d entr%s that recorded one (avg %d min)\n",
			humanMinutes(st.CostMin), st.Costed, plural(st.Costed, "y", "ies"), st.CostMin/max(st.Costed, 1))
	}

	if len(st.Months) > 0 {
		b.WriteString("\n## By month\n")
		for _, m := range st.Months {
			fmt.Fprintf(&b, "  %s  %-3d %s\n", m.Month, m.Count, strings.Repeat("█", m.Count))
		}
	}

	if len(st.Tags) > 0 {
		b.WriteString("\n## By tag (a cluster is what argues for a tool fix)\n")
		for _, t := range st.Tags {
			open := ""
			if t.Open > 0 {
				open = fmt.Sprintf("  (%d open)", t.Open)
			}
			fmt.Fprintf(&b, "  %-24s %d%s\n", t.Tag, t.Count, open)
		}
	}

	if len(st.OpenList) > 0 {
		b.WriteString("\n## Open - no fix recorded yet\n")
		for _, in := range st.OpenList {
			fmt.Fprintf(&b, "  %s  %s\n", journalDate(in), in.Symptom)
			if in.Cause != "" {
				fmt.Fprintf(&b, "              cause: %s\n", in.Cause)
			}
		}
	}
	return b.String()
}

func shortDate(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("2006-01-02")
	}
	return ts
}

func humanMinutes(m int) string {
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	return fmt.Sprintf("%dh%02d", m/60, m%60)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
