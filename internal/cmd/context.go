package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mbalazy/pm-cli/internal/projectarg"
	"github.com/mbalazy/pm-cli/internal/service"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/spf13/cobra"
)

func newContextCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "context [project]",
		Short: "Show session context: trackers (parent+subtask rollup), active tasks, counts",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := projectarg.Slug(store, args)
			if err != nil {
				return err
			}

			// The rollup is the service's - the one pm_context returns - and
			// this command only renders it as text. It needs the tasks alone,
			// so an unreadable project.yaml costs only the pointers below.
			rollup, err := service.ProjectTaskRollup(store, slug)
			if err != nil {
				return err
			}
			trackers, doing, counts := rollup.Trackers, rollup.DoingTasks, rollup.TaskCounts

			fmt.Printf("# %s\n\n", slug)

			if len(trackers) > 0 {
				for _, tr := range trackers {
					fmt.Printf("📋 %s  [%s]  (%s)\n", tr.ID, tr.Status, progressLine(tr))
					fmt.Printf("   %s\n", tr.Title)
					if tr.ChildrenOmitted {
						fmt.Println("   (finished - children collapsed)")
					}
					for _, c := range tr.Children {
						mark := statusMark(c.Status)
						line := fmt.Sprintf("   %s %-13s %-7s o:%-4d", mark, c.ID, c.Status, c.Order)
						if c.Branch != "" {
							line += "  " + c.Branch
						}
						fmt.Println(line)
						if c.BriefLine != "" {
							fmt.Printf("        └ %s\n", c.BriefLine)
						}
					}
					fmt.Println()
				}
			}

			if len(doing) > 0 {
				fmt.Println("## Doing (standalone)")
				for _, t := range doing {
					fmt.Printf("  • %s  %s\n", t.ID, t.Title)
				}
				fmt.Println()
			}

			fmt.Printf("## Counts: %s\n", countsLine(counts))
			// One line, only when there is a profile to point at. `pm context`
			// runs at the start of EVERY session, so the full profile belongs in
			// `pm executor show` - only whoever needs it pays for it.
			proj, projErr := store.GetProject(slug)
			if projErr == nil && proj.HasExecutor() {
				fmt.Printf("## Executor: pm executor show %s\n", slug)
			}
			// Same rule as the executor profile above: a POINTER, never the
			// content. A journal only grows, and this runs at the start of
			// every session - the counts are enough to notice something is
			// accumulating, and whoever is about to touch the subsystem pulls
			// the entries themselves.
			if projErr == nil {
				if counts, cerr := storage.JournalCounts(store.ProjectDir(slug), proj); cerr == nil && len(counts) > 0 {
					fmt.Printf("## Journals: %s\n", journalPointer(counts))
				}
			}
			return nil
		},
	}
}

func progressLine(tr storage.Tracker) string {
	keys := make([]string, 0, len(tr.Progress))
	for k := range tr.Progress {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", tr.Progress[k], k))
	}
	return fmt.Sprintf("%d total: %s", tr.Total, strings.Join(parts, ", "))
}

func countsLine(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return strings.Join(parts, ", ")
}

func statusMark(status string) string {
	switch status {
	case "done":
		return "✅"
	case string(storage.StatusMerged):
		return "🔀"
	case string(storage.StatusPushed):
		return "📤"
	case "doing":
		return "🔨"
	case "waiting":
		return "⏳"
	default:
		// Emoji marks (✅ 🔀 🔨 ⏳) render as 2 terminal columns; "○" alone is
		// only 1. Pad it with a trailing space so every mark occupies the
		// same 2-column width and the ID/status/order columns stay aligned.
		return "○ "
	}
}
