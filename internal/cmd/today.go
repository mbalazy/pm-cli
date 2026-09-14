package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mbalazy/pm/internal/service"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

// `pm today`: the attention queue as text - the CLI face of the cockpit's
// home screen and of pm_context's attention block, off the same
// aggregation (service.Attention), so a terminal, the browser and a Claude
// session never disagree about what needs the user.

func newTodayCmd(store storage.TaskStore) *cobra.Command {
	var (
		asJSON  bool
		project string
		group   string
	)
	cmd := &cobra.Command{
		Use:   "today",
		Short: "What needs you across every project: failed runs, open acceptances, focus, blockers, stuck projects",
		Long: "Prints the attention queue - the cockpit's home screen in text. Sections in a fixed order " +
			"(needs_me, landed_no_pr, focus, in_progress, waiting, changes, stuck_projects, and the opt-in " +
			"new_since_cutoff/recent), each on/off and every threshold from the `cockpit:` block of the global " +
			"config (`pm config show`). One line per row: <glyph> <project> <id> <title> · <reason> · <age>.\n\n" +
			"--json prints exactly what `pm serve` answers on /api/attention.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := service.Attention(store, service.AttentionInput{Project: project, Group: group})
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(a)
			}
			renderAttention(cmd.OutOrStdout(), a)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit JSON (the /api/attention shape) instead of text")
	cmd.Flags().StringVar(&project, "project", "", "Only this project's rows")
	cmd.Flags().StringVar(&group, "group", "", "Only this group's rows (every member project)")
	return cmd
}

// sectionGlyphs are the one-character marks the text surface prefixes rows
// with, per section. The browser has colour; the terminal has these.
var sectionGlyphs = map[string]string{
	storage.SectionNeedsMe:        "!",
	storage.SectionSoloReports:    "✎",
	storage.SectionLandedNoPR:     "⇡",
	storage.SectionFocus:          "★",
	storage.SectionInProgress:     "▶",
	storage.SectionWaiting:        "⧗",
	storage.SectionChanges:        "Δ",
	storage.SectionStuckProjects:  "◔",
	storage.SectionNewSinceCutoff: "+",
	storage.SectionRecent:         "·",
}

// sectionTitles are the section headers, in the user's words.
var sectionTitles = map[string]string{
	storage.SectionNeedsMe:        "needs me",
	storage.SectionSoloReports:    "solo reports",
	storage.SectionLandedNoPR:     "accepted, no PR",
	storage.SectionFocus:          "focus",
	storage.SectionInProgress:     "in progress",
	storage.SectionWaiting:        "waiting on",
	storage.SectionChanges:        "changes",
	storage.SectionStuckProjects:  "stuck projects",
	storage.SectionNewSinceCutoff: "new since cutoff",
	storage.SectionRecent:         "recently touched",
}

// renderAttention writes the queue as text. Pure over the value, so it is
// table-testable and `pm today` output is one function away from the JSON.
func renderAttention(w io.Writer, a *storage.Attention) {
	fmt.Fprintf(w, "# today  (wip %d, generated %s)\n", a.WIP, storage.StampDate(a.Generated))
	for _, sec := range a.Sections {
		title := sectionTitles[sec.Name]
		if title == "" {
			title = sec.Name
		}
		fmt.Fprintf(w, "\n## %s (%d)\n", title, sec.Total)
		if sec.Note != "" {
			fmt.Fprintf(w, "  (%s)\n", sec.Note)
		}
		glyph := sectionGlyphs[sec.Name]
		if glyph == "" {
			glyph = "-"
		}
		for _, r := range sec.Rows {
			fmt.Fprintln(w, "  "+attentionLine(glyph, r))
		}
	}
	if len(a.Groups) > 0 {
		fmt.Fprintln(w, "\n## groups")
		for _, g := range a.Groups {
			fmt.Fprintf(w, "  %-5s %-16s ✗%d 👁%d ⧗%d ◔%d  %s\n", g.Worst, g.Name, g.Failed, g.Visual, g.Waiting, g.Quiet, strings.Join(g.Projects, ","))
		}
	}
}

// attentionLine is the one-line row: <glyph> <project> <id> <title> · <reason> · <age>.
func attentionLine(glyph string, r storage.AttentionRow) string {
	parts := []string{glyph, r.Project}
	if r.TaskID != "" {
		parts = append(parts, r.TaskID)
	}
	if r.Title != "" && r.Title != r.Project {
		parts = append(parts, r.Title)
	}
	line := strings.Join(parts, " ") + " · " + r.Reason + " · " + r.AgeString()
	if len(r.Flags) > 0 {
		line += "  [" + strings.Join(r.Flags, ",") + "]"
	}
	return line
}
