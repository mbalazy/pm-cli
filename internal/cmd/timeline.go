package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// `pm timeline` is the terminal surface of the project timeline (see
// internal/storage/timeline.go): what happened TO a project, plus dated state
// snapshots of where it stands.

func newTimelineCmd(store storage.TaskStore) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "timeline [project]",
		Short: "What happened to a project and where it stands (latest state + every entry since)",
		Long: "A project timeline records what happened TO the project - a direction change, a client decision, " +
			"a milestone, a new person, a document arriving - as append-only entries of three kinds:\n\n" +
			"    event     something happened (one to three sentences)\n" +
			"    decision  one sentence plus a ref to the full record (a task, a doc)\n" +
			"    state     a dated snapshot of where the project stands; fits on a screen (10-20 lines:\n" +
			"              where we stand, what blocks, what is next, open questions, links)\n\n" +
			"What happened INSIDE a task belongs in that task's Log, not here.\n\n" +
			"A state line says where it comes from: end it with [verified YYYY-MM-DD by <command or doc>] or [assumed]. " +
			"The read counts the lines (verified / to recheck / assumed / unmarked - an unmarked line is never read as " +
			"verified) and lists the verified lines " + fmt.Sprint(storage.TimelineRecheckDays) + "+ days old as due for a re-check.\n\n" +
			"A state is never overwritten: a new state is a new entry and older ones stay. With no subcommand " +
			"this prints the default read - the latest state in full plus every entry after it, oldest first - " +
			"and a stale: line once " + fmt.Sprint(storage.TimelineStaleEntries) + " entries or " +
			fmt.Sprint(storage.TimelineStaleDays) + " days have passed since that state, which is the cue to write a new one.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := timelineProject(cmd, store, args)
			if err != nil {
				return err
			}
			r, err := storage.ReadTimelineDefault(store.ProjectDir(slug), time.Now())
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), r)
			}
			fmt.Fprint(cmd.OutOrStdout(), renderTimelineRead(slug, r))
			return nil
		},
	}
	cmd.PersistentFlags().StringP("project", "p", "", "project slug (default: detected from cwd)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the default read as one JSON object")
	cmd.AddCommand(newTimelineAddCmd(store), newTimelineListCmd(store))
	return cmd
}

// timelineProject resolves the project from the positional argument, else
// --project, else the cwd.
func timelineProject(cmd *cobra.Command, store storage.TaskStore, args []string) (string, error) {
	if len(args) == 0 {
		if flag, _ := cmd.Flags().GetString("project"); flag != "" {
			args = []string{flag}
		}
	}
	return resolveProjectSlugArg(store, args)
}

func newTimelineAddCmd(store storage.TaskStore) *cobra.Command {
	var e storage.TimelineEntry
	var date string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Append an entry to the project timeline",
		Long: "Appends one entry. --kind is event, decision or state; --text is the entry (\"-\" reads it from " +
			"stdin, the way to pass a multi-line state). A state fits on a screen: 10-20 lines saying where the " +
			"project stands, what blocks, what is next, open questions and links - longer content stays in a " +
			"document the state refers to.\n\n" +
			"End each state line with its provenance: [verified YYYY-MM-DD by <command or doc>] for a fact you checked " +
			"in this session, [assumed] for one you did not. A [verified] marker without a day, or with a day in the " +
			"future, is refused. Lines with no marker are counted as unverified on every read.\n\n" +
			"Entries are never edited. A changed picture is a new state, not a rewrite of the old one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := timelineProject(cmd, store, nil)
			if err != nil {
				return err
			}
			if e.Text == "-" {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read text from stdin: %w", err)
				}
				e.Text = string(data)
			}
			if date != "" {
				ts, err := storage.TimelineBackdate(date, time.Now())
				if err != nil {
					return err
				}
				e.TS = ts
			}
			e.Session = timelineSession(e.Session)
			dir := store.ProjectDir(slug)
			if err := storage.AppendTimelineEntry(dir, &e); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s: %s added (%s, id %s)\n", slug, e.Kind, timelineDate(e), e.ID)
			if r, err := storage.ReadTimelineDefault(dir, time.Now()); err == nil {
				if e.Kind == storage.TimelineState && r.Verification != nil {
					fmt.Fprintln(out, verificationSummary(*r.Verification))
					if r.Verification.Note != "" {
						fmt.Fprintln(out, r.Verification.Note)
					}
				}
				if r.Stale {
					fmt.Fprintln(out, r.Note)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&e.Kind, "kind", "", "event, decision or state (required)")
	cmd.Flags().StringVar(&e.Text, "text", "", "the entry text; \"-\" reads it from stdin (required)")
	// An array, not a slice flag: a slice flag splits on commas, and a URL
	// with a comma in its query is still one reference.
	cmd.Flags().StringArrayVar(&e.Refs, "ref", nil, "a reference - task id, path or URL, stored verbatim (repeatable)")
	cmd.Flags().StringVar(&e.Session, "session", "", "Claude session id (default: detected when run inside Claude Code)")
	cmd.Flags().StringVar(&date, "date", "", "back-date the entry (YYYY-MM-DD) when seeding history - midnight of that day; "+
		"today's date takes the current time, the same as no flag, so an entry written now never sorts before this morning's")
	return cmd
}

// timelineSession returns the explicit session id, else the current Claude
// Code session when the command runs inside one, else "".
func timelineSession(explicit string) string {
	if explicit != "" || os.Getenv("CLAUDECODE") != "1" {
		return explicit
	}
	if sid, err := detectSession(); err == nil {
		return sid
	}
	return ""
}

func newTimelineListCmd(store storage.TaskStore) *cobra.Command {
	var since, kind string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List timeline entries, newest first, across every month",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := timelineProject(cmd, store, nil)
			if err != nil {
				return err
			}
			var from time.Time
			if since != "" {
				if from, err = time.ParseInLocation("2006-01-02", since, time.Local); err != nil {
					return fmt.Errorf("--since %q: want YYYY-MM-DD", since)
				}
			}
			if kind != "" {
				if err := storage.ValidateTimelineKind(kind); err != nil {
					return err
				}
			}
			entries, err := storage.ReadTimeline(store.ProjectDir(slug))
			if err != nil {
				return err
			}
			matched := storage.FilterTimeline(entries, from, kind)
			if asJSON {
				return printJSON(cmd.OutOrStdout(), struct {
					Entries []storage.TimelineEntry `json:"entries"`
					Matched int                     `json:"matched"`
					Total   int                     `json:"total"`
				}{matched, len(matched), len(entries)})
			}
			fmt.Fprint(cmd.OutOrStdout(), renderTimelineList(slug, matched, len(entries)))
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "only entries on or after this day (YYYY-MM-DD)")
	cmd.Flags().StringVar(&kind, "kind", "", "only entries of this kind (event, decision, state)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// timelineDate renders the day in the reader's zone.
func timelineDate(e storage.TimelineEntry) string {
	if t, ok := e.When(); ok {
		return t.Local().Format("2006-01-02")
	}
	return "??????????"
}

// timelineIndent aligns continuation lines under the text column of
// renderTimelineEntry ("2006-01-02  decision  ").
const timelineIndent = "                      "

func renderTimelineEntry(b *strings.Builder, e storage.TimelineEntry) {
	lines := strings.Split(e.Text, "\n")
	fmt.Fprintf(b, "%s  %-8s  %s\n", timelineDate(e), e.Kind, lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintf(b, "%s%s\n", timelineIndent, l)
	}
	if len(e.Refs) > 0 {
		fmt.Fprintf(b, "%srefs: %s\n", timelineIndent, strings.Join(e.Refs, ", "))
	}
}

func renderTimelineRead(slug string, r storage.TimelineRead) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# timeline: %s\n\n", slug)
	if r.Total == 0 {
		b.WriteString("no timeline entries yet.\n")
		b.WriteString("  pm timeline add --kind state --text -    (where the project stands, 10-20 lines)\n")
		return b.String()
	}
	if r.State == nil {
		fmt.Fprintf(&b, "%s\n\n", r.Note)
		for _, e := range r.Since {
			renderTimelineEntry(&b, e)
		}
		return b.String()
	}

	fmt.Fprintf(&b, "## state %s  (id %s)\n", timelineDate(*r.State), r.State.ID)
	renderStateText(&b, r.State.Text, r.Verification)
	if len(r.State.Refs) > 0 {
		fmt.Fprintf(&b, "refs: %s\n", strings.Join(r.State.Refs, ", "))
	}
	if r.Verification != nil && r.Verification.Note != "" {
		fmt.Fprintln(&b, r.Verification.Note)
	}
	fmt.Fprintf(&b, "\n## since the state (%d)\n", r.EntriesSince)
	if len(r.Since) == 0 {
		b.WriteString("nothing since the state.\n")
	}
	for _, e := range r.Since {
		renderTimelineEntry(&b, e)
	}
	if r.Stale {
		fmt.Fprintf(&b, "\n%s\n", r.Note)
	}
	return b.String()
}

// verificationSummary is the one-line count under a state header:
// "verification: 3 verified, 2 to recheck (7+ days), 1 assumed, 4 unmarked".
func verificationSummary(v storage.TimelineVerification) string {
	return fmt.Sprintf("verification: %d verified, %d to recheck (%d+ days), %d assumed, %d unmarked",
		v.Verified, v.Recheck, storage.TimelineRecheckDays, v.Assumed, v.Unmarked)
}

// renderStateText prints a state's text under its verification summary. A
// state with markers gets a gutter - " ! " on a line due for a re-check (its
// age appended), " ~ " on an assumed line, "   " otherwise - so the lines to
// act on stand out in the 40 lines the post-compaction hook prints; a state
// with no markers prints as it always did under the summary. The
// verification note follows the refs, printed by the caller.
func renderStateText(b *strings.Builder, text string, v *storage.TimelineVerification) {
	if v == nil {
		fmt.Fprintf(b, "%s\n", text)
		return
	}
	fmt.Fprintln(b, verificationSummary(*v))
	if !v.Marked() {
		fmt.Fprintf(b, "%s\n", text)
		return
	}
	due := map[int]storage.StateLine{}
	for _, sl := range v.RecheckLines {
		due[sl.N] = sl
	}
	assumed := map[int]bool{}
	for _, sl := range storage.ParseStateLines(text) {
		if sl.Mark == storage.VerifyAssumed {
			assumed[sl.N] = true
		}
	}
	for i, line := range strings.Split(text, "\n") {
		switch {
		case due[i+1].Recheck:
			fmt.Fprintf(b, " ! %s   <- %d days\n", line, due[i+1].Days)
		case assumed[i+1]:
			fmt.Fprintf(b, " ~ %s\n", line)
		default:
			fmt.Fprintf(b, "   %s\n", line)
		}
	}
}

func renderTimelineList(slug string, entries []storage.TimelineEntry, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# timeline: %s  (%d of %d entries, newest first)\n\n", slug, len(entries), total)
	if len(entries) == 0 {
		b.WriteString("no matching entries.\n")
	}
	for _, e := range entries {
		renderTimelineEntry(&b, e)
	}
	return b.String()
}
