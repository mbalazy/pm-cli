package cmd

import (
	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/version"
	"github.com/spf13/cobra"
)

func newStore() storage.TaskStore {
	return storage.NewStore()
}

func NewRootCmd() *cobra.Command {
	store := newStore()

	root := &cobra.Command{
		Use:     "pm",
		Short:   "Local project manager for freelancers",
		Long:    "A local, file-based project manager with interactive TUI board. Integrates with Claude Code.",
		Version: version.Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			// default: open board, auto-detect project from cwd
			return runBoard(store, detectProjectFromCwd(store))
		},
		SilenceUsage: true,
	}

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
		newMcpCmd(store),
		newMigrateIDsCmd(store),
		newSessionIDCmd(),
	)

	return root
}
