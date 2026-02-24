package cmd

import (
	"fmt"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newInitCmd(store *storage.Store) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize pm storage directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := store.Init(); err != nil {
				return err
			}
			fmt.Printf("Initialized pm at %s\n", store.Root)
			return nil
		},
	}
}
