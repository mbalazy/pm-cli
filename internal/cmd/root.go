package cmd

import (
	"os"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/version"
	"github.com/spf13/cobra"
)

// newStore creates the appropriate TaskStore based on environment.
// If PM_SYNC_URL is set, wraps with SyncStore for background sync.
func newStore() storage.TaskStore {
	s := storage.NewStore()
	syncURL := os.Getenv("PM_SYNC_URL")
	if syncURL == "" {
		return s
	}
	token := os.Getenv("PM_SYNC_TOKEN")
	return storage.NewSyncStore(s, syncURL, token)
}

func NewRootCmd() *cobra.Command {
	store := newStore()

	root := &cobra.Command{
		Use:     "pm",
		Short:   "Local project manager for freelancers",
		Long:    "A local, file-based project manager with interactive TUI board. Integrates with Claude Code.",
		Version: version.Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			// default: open board
			return runBoard(store, "")
		},
		SilenceUsage: true,
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			// Graceful shutdown of SyncStore flush goroutine
			if ss, ok := store.(*storage.SyncStore); ok {
				ss.Close()
			}
		},
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
		newSyncCmd(store),
	)

	return root
}
