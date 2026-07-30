package cmd

import (
	"fmt"

	"github.com/mbalazy/pm/docs"
	"github.com/spf13/cobra"
)

// newDocsCmd exposes the embedded agent documentation. The docs travel inside
// the binary so the usage contract is installable anywhere pm is - and always
// matches the installed version, unlike a hand-copied CLAUDE.md block.
func newDocsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Print embedded agent documentation",
		Long: "Prints the canonical documentation for AI agents using pm, embedded in the binary.\n\n" +
			"  pm docs claude     the usage contract for agents, shaped as a CLAUDE.md block\n" +
			"  pm docs authoring  task-authoring rules (referenced by the guide)\n\n" +
			"Install the guide into Claude Code's global memory:\n\n" +
			"  pm docs claude >> ~/.claude/CLAUDE.md\n\n" +
			"The block is wrapped in <!-- pm:agent-guide:start/end --> markers; to refresh\n" +
			"after an upgrade, delete the block and append again.",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "claude",
			Short: "Print the agent usage contract (append to ~/.claude/CLAUDE.md)",
			Args:  cobra.NoArgs,
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
