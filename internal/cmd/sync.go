package cmd

import (
	"fmt"
	"os"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newSyncCmd(store storage.TaskStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Manually flush sync queue to remote",
		Long:  "Push all pending local changes to the pm-sync API. Requires PM_SYNC_URL to be set.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ss, ok := store.(*storage.SyncStore)
			if !ok {
				fmt.Fprintln(os.Stderr, "Sync not enabled. Set PM_SYNC_URL to enable.")
				return nil
			}

			pending := ss.QueueLen()
			if pending == 0 {
				fmt.Println("Queue empty, nothing to sync.")
				return nil
			}

			fmt.Printf("Flushing %d pending operations...\n", pending)
			if err := ss.Flush(); err != nil {
				return fmt.Errorf("sync failed: %w", err)
			}

			remaining := ss.QueueLen()
			if remaining > 0 {
				fmt.Printf("Done. %d operations failed and will retry.\n", remaining)
			} else {
				fmt.Println("Done. All synced.")
			}
			return nil
		},
	}

	cmd.AddCommand(newSyncStatusCmd(store))
	return cmd
}

func newSyncStatusCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync queue status",
		Run: func(cmd *cobra.Command, args []string) {
			ss, ok := store.(*storage.SyncStore)
			if !ok {
				fmt.Println("Sync not enabled. Set PM_SYNC_URL to enable.")
				return
			}
			pending := ss.QueueLen()
			syncURL := os.Getenv("PM_SYNC_URL")
			fmt.Printf("Sync URL: %s\n", syncURL)
			fmt.Printf("Pending:  %d operations\n", pending)
		},
	}
}
