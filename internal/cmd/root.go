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
		Short:   "Local project manager and control plane for AI coding agents",
		Long:    "A local, file-based project manager with an interactive TUI board, an MCP server for Claude Code, and an autonomous executor that runs tasks through headless workers.",
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
		newContextCmd(store),
		newShowCmd(store),
		newMvCmd(store),
		newReorderCmd(store),
		newDoneCmd(store),
		newEditCmd(store),
		newBoardCmd(store),
		newWorkCmd(store),
		newRunEpicCmd(store),
		newExecutorCmd(store),
		newMcpCmd(store),
		newMigrateIDsCmd(store),
		newSessionIDCmd(),
		newDocsCmd(),
	)

	return root
}
