package cmd

import (
	"fmt"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newConfigCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Global pm configuration (remote runners, cockpit settings)",
		Long: "The global pm config lives at <pm data dir>/config.yaml - one file next to the projects, " +
			"not a copy inside each project.yaml, because one remote runner serves many projects and the " +
			"cockpit's settings (group names, thresholds, refresh window, section and source toggles) " +
			"describe how you work, not one repo.\n\n" +
			"The remotes block is hand-authored; the cockpit block is edited by the cockpit's settings screen " +
			"or by hand, and pm writes it back with your comments and unknown keys kept.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newConfigShowCmd(store))
	return cmd
}

func newConfigShowCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the resolved global config and the file it came from",
		Long: "Prints the global config as RESOLVED, not as written (the pattern `pm executor show` follows): " +
			"the file it was read from, every remote runner pm knows about, and the cockpit block with its " +
			"defaults filled in.\n\n" +
			"No file at all is the normal state and prints as zero remote runners. An unreadable one is an " +
			"error - a YAML typo must not read as \"you have no remotes\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := store.LoadConfig()
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), renderPMConfig(cfg))
			return nil
		},
	}
}

// renderPMConfig renders the resolved config. Pure, so it is table-testable.
func renderPMConfig(cfg *storage.PMConfig) string {
	var b strings.Builder
	b.WriteString("# pm config\n")
	if cfg == nil || !cfg.Exists {
		path := storage.ConfigFileName
		if cfg != nil {
			path = cfg.Path
		}
		fmt.Fprintf(&b, "source: %s  [no file - zero remote runners, cockpit defaults]\n", path)
		if cfg != nil {
			renderCockpitConfig(&b, &cfg.Cockpit)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "source: %s\n", cfg.Path)

	if len(cfg.Remotes) == 0 {
		b.WriteString("\n## Remote runners\n  (none declared)\n")
	} else {
		fmt.Fprintf(&b, "\n## Remote runners (%d)\n", len(cfg.Remotes))
		for _, r := range cfg.Remotes {
			fmt.Fprintf(&b, "  %s\n", r.Name)
			fmt.Fprintf(&b, "    ssh:  %s\n", r.SSH)
			fmt.Fprintf(&b, "    pm:   %s\n", r.PM)
			fmt.Fprintf(&b, "    root: %s\n", r.Root)
		}
	}
	renderCockpitConfig(&b, &cfg.Cockpit)
	return b.String()
}

// renderCockpitConfig prints the cockpit block as resolved: a missing block
// prints its defaults, which is the honest answer to "what will the cockpit
// do" (the defaults are what it does).
func renderCockpitConfig(b *strings.Builder, c *storage.CockpitConfig) {
	b.WriteString("\n## Cockpit\n")
	if len(c.Groups) == 0 {
		b.WriteString("  groups: (none named - every group displays as its slug)\n")
	} else {
		fmt.Fprintf(b, "  groups (%d, manual sidebar order)\n", len(c.Groups))
		for _, slug := range c.GroupOrder() {
			if o := c.Groups[slug].Order; o > 0 {
				fmt.Fprintf(b, "    %d. %s: %s\n", o, slug, c.GroupName(slug))
			} else {
				fmt.Fprintf(b, "    -  %s: %s\n", slug, c.GroupName(slug))
			}
		}
	}
	fmt.Fprintf(b, "  thresholds: doing idle %dd · waiting highlight %dd · project stuck %dd\n",
		c.DoingIdleDays, c.WaitingHighlightDays, c.StuckProjectDays)
	fmt.Fprintf(b, "  cutoff hour: %02d:00\n", c.CutoffHour)
	fmt.Fprintf(b, "  refresh: every %s within %s\n", c.Refresh.Every, c.Refresh.Window)
	fmt.Fprintf(b, "  sections: %s\n", toggles(storage.CockpitSections, c.Sections))
	fmt.Fprintf(b, "  sources: %s\n", toggles(storage.CockpitSources, c.Sources))
	fmt.Fprintf(b, "  sidebar: %s · repos %s · sort %s · width %s\n",
		c.Sidebar.Variant, onOff(c.Sidebar.ShowRepos), c.Sidebar.Sort, widthOrDefault(c.Sidebar.Width))
	fmt.Fprintf(b, "  git: all branches %s\n", onOff(c.Git.AllBranches))
	fmt.Fprintf(b, "  report: %s · model %s · language %s\n", onOff(c.Sources["report"]), c.Report.Model, c.Report.Language)
	if len(c.Slack.Servers) == 0 {
		fmt.Fprintf(b, "  slack: %s · no servers (cockpit.slack.servers)\n", onOff(c.Sources["slack"]))
	} else {
		fmt.Fprintf(b, "  slack: %s · %d server(s)\n", onOff(c.Sources["slack"]), len(c.Slack.Servers))
		for _, sv := range c.Slack.Servers {
			src := sv.Command
			if sv.ClaudeServer != "" {
				src = "claude_server " + sv.ClaudeServer
			}
			me := sv.Me
			if me == "" {
				me = "(no me - mentions/DMs skipped)"
			}
			fmt.Fprintf(b, "    %s: %s · me %s\n", sv.Workspace, src, me)
		}
	}
}

// toggles renders a toggle map in the closed list's order, "name" for on and
// "-name" for off, so the line reads as the full switchboard.
func toggles(order []string, m map[string]bool) string {
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if m[name] {
			parts = append(parts, name)
		} else {
			parts = append(parts, "-"+name)
		}
	}
	return strings.Join(parts, " ")
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func widthOrDefault(w int) string {
	if w == 0 {
		return "default"
	}
	return fmt.Sprintf("%dpx", w)
}
