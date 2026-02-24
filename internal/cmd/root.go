package cmd

import (
	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "pm",
		Short: "Local project manager for freelancers",
		Long:  "A local, file-based project manager with interactive TUI board. Integrates with Claude Code.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// default: open board
			return runBoard(storage.NewStore(), "")
		},
		SilenceUsage: true,
	}

	store := storage.NewStore()

	root.AddCommand(
		newInitCmd(store),
		newProjectsCmd(store),
		newAddCmd(store),
		newListCmd(store),
		newShowCmd(store),
		newMvCmd(store),
		newDoneCmd(store),
		newEditCmd(store),
		newBoardCmd(store),
	)

	return root
}
