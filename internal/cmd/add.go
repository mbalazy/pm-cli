package cmd

import (
	"fmt"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newAddCmd(store storage.TaskStore) *cobra.Command {
	var status, branch string
	var links map[string]string
	var tags []string

	cmd := &cobra.Command{
		Use:   "add <project> <title>",
		Short: "Add a new task to a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectSlug, err := store.ResolveProject(args[0])
			if err != nil {
				return err
			}

			title := args[1]
			t := storage.NewTask("", title, projectSlug)

			if status != "" {
				t.Meta.Status = storage.ParseStatus(status)
			}

			// Validate status against project's allowed statuses
			allowed := store.GetProjectStatuses(projectSlug)
			if err := storage.ValidateStatus(t.Meta.Status, allowed); err != nil {
				return err
			}

			if id, _ := cmd.Flags().GetString("id"); id != "" {
				t.Meta.ID = id
			} else {
				t.Meta.ID = store.NextTaskID(projectSlug)
			}

			if len(links) > 0 {
				t.Meta.Links = links
			}
			t.Meta.Branch = branch
			t.Meta.Tags = tags

			if err := store.AddTask(projectSlug, t); err != nil {
				return err
			}

			fmt.Printf("Created task: %s → %s\n", t.Meta.ID, t.FilePath)
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "todo", "Task status (todo, doing, done)")
	cmd.Flags().String("id", "", "Task ID (auto-generated if not set)")
	cmd.Flags().StringToStringVarP(&links, "link", "l", nil, "Links (key=url, repeatable: -l azure=https://...)")
	cmd.Flags().StringVar(&branch, "branch", "", "Git branch name")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags")

	return cmd
}
