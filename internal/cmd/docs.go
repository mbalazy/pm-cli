package cmd

import (
	"fmt"

	"github.com/mbalazy/pm-cli/docs"
	"github.com/spf13/cobra"
)

// newDocsCmd exposes the embedded agent documentation. The docs travel inside
// the binary so the usage contract is installable anywhere pm is - and always
// matches the installed version, unlike a hand-copied CLAUDE.md or AGENTS.md
// block.
func newDocsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Print embedded agent documentation",
		Long: "Prints the canonical documentation for AI agents using pm, embedded in the binary.\n\n" +
			"  pm docs guide      the usage contract for agents, one markdown block that any\n" +
			"                     MCP client's instructions file takes (`pm docs claude` is an alias)\n" +
			"  pm docs authoring  task-authoring rules (referenced by the guide)\n\n" +
			"Install the guide into the instructions file your agent reads:\n\n" +
			"  pm docs guide >> ~/.claude/CLAUDE.md   # Claude Code\n" +
			"  pm docs guide >> ~/.codex/AGENTS.md    # Codex\n\n" +
			"The block is wrapped in <!-- pm:agent-guide:start/end --> markers; to refresh\n" +
			"after an upgrade, delete the block and append again.",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:     "guide",
			Aliases: []string{"claude"},
			Short:   "Print the agent usage contract (append to CLAUDE.md or AGENTS.md)",
			Args:    cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Fprint(cmd.OutOrStdout(), docs.AgentGuide)
				return nil
			},
		},
		&cobra.Command{
			Use:   "authoring",
			Short: "Print the task-authoring rules",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				fmt.Fprint(cmd.OutOrStdout(), docs.TaskAuthoring)
				return nil
			},
		},
	)

	return cmd
}
