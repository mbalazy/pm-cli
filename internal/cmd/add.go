package cmd

import (
	"fmt"

	"github.com/mbalazy/pm-cli/internal/service"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/spf13/cobra"
)

func newAddCmd(store storage.TaskStore) *cobra.Command {
	var status, branch string
	var links map[string]string
	var tags []string
	var order int

	cmd := &cobra.Command{
		Use:   "add <project> <title>",
		Short: "Add a new task to a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The service owns task creation for every surface - the MCP add
			// tool, the web, this command - so a task gets the same defaults
			// (the project's FIRST status when --status is not given), the
			// same validation and the same stamping wherever it is created.
			// It also takes the project lock around NextTaskID -> AddTask:
			// the id is derived from the tasks on disk, so two unlocked adds
			// would mint the SAME id, and O_EXCL only protects the file NAME.
			id, _ := cmd.Flags().GetString("id")
			t, err := service.AddTask(store, service.AddTaskInput{
				Project: args[0],
				Title:   args[1],
				Status:  status,
				ID:      id,
				Links:   links,
				Branch:  branch,
				Tags:    tags,
				Order:   order,
			})
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Created task: %s → %s\n", t.Meta.ID, t.FilePath)
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "Task status (default: the project's first status)")
	cmd.Flags().String("id", "", "Task ID (auto-generated if not set)")
	cmd.Flags().StringToStringVarP(&links, "link", "l", nil, "Links (key=url, repeatable: -l azure=https://...)")
	cmd.Flags().StringVar(&branch, "branch", "", "Git branch name")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags")
	cmd.Flags().IntVar(&order, "order", 0, "Sort order within column/rollup (lower first; convention 10, 20, 30...)")

	return cmd
}
