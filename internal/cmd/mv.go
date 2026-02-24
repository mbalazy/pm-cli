package cmd

import (
	"fmt"
	"time"

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
			task.Meta.Status = newStatus
			task.Meta.Updated = time.Now().Format("2006-01-02")
			if err := storage.WriteTask(task); err != nil {
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
			task.Meta.Status = storage.StatusDone
			task.Meta.Updated = time.Now().Format("2006-01-02")
			if err := storage.WriteTask(task); err != nil {
				return err
			}

			fmt.Printf("%s #%s: %s → done\n", task.Meta.Title, task.Meta.ID, old)
			return nil
		},
	}
}
