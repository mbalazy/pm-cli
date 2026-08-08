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
		Short: "Global pm configuration (remote runners)",
		Long: "The global pm config lives at <pm data dir>/config.yaml - one file next to the projects, " +
			"not a copy inside each project.yaml, because one remote runner serves many projects.\n\n" +
			"pm never writes this file; it is hand-authored.",
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
			"the file it was read from, and every remote runner pm knows about.\n\n" +
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
		fmt.Fprintf(&b, "source: %s  [no file - zero remote runners]\n", path)
		return b.String()
	}
	fmt.Fprintf(&b, "source: %s\n", cfg.Path)

	if len(cfg.Remotes) == 0 {
		b.WriteString("\n## Remote runners\n  (none declared)\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n## Remote runners (%d)\n", len(cfg.Remotes))
	for _, r := range cfg.Remotes {
		fmt.Fprintf(&b, "  %s\n", r.Name)
		fmt.Fprintf(&b, "    ssh:  %s\n", r.SSH)
		fmt.Fprintf(&b, "    pm:   %s\n", r.PM)
		fmt.Fprintf(&b, "    root: %s\n", r.Root)
	}
	return b.String()
}
