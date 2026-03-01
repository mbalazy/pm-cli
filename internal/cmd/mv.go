package cmd

import (
	"fmt"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newMvCmd(store *storage.Store) *cobra.Command {
	return &cobra.Command{
		Use:   "mv <project> <task-id> <status>",
		Short: "Move task to a different status",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := store.ResolveProject(args[0])
			if err != nil {
				return err
			}
			task, err := store.FindTask(slug, args[1])
			if err != nil {
				return err
			}
			newStatus := storage.ParseStatus(args[2])
			old := task.Meta.Status
			if err := store.MoveTask(task, newStatus); err != nil {
				return err
			}

			fmt.Printf("%s #%s: %s → %s\n", task.Meta.Title, task.Meta.ID, old, newStatus)
			return nil
		},
	}
}

func newDoneCmd(store *storage.Store) *cobra.Command {
	return &cobra.Command{
		Use:   "done <project> <task-id>",
		Short: "Mark task as done",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := store.ResolveProject(args[0])
			if err != nil {
				return err
			}
			task, err := store.FindTask(slug, args[1])
			if err != nil {
				return err
			}

			old := task.Meta.Status
			if err := store.MoveTask(task, storage.StatusDone); err != nil {
				return err
			}

			fmt.Printf("%s #%s: %s → done\n", task.Meta.Title, task.Meta.ID, old)
			return nil
		},
	}
}
