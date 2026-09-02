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
	var order int

	cmd := &cobra.Command{
		Use:   "add <project> <title>",
		Short: "Add a new task to a project",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectSlug, err := store.ResolveProject(args[0])
			if err != nil {
				return err
			}

			// Serialize NextTaskID -> AddTask against other pm processes
			// (parallel `pm add`, a CC session's MCP server, the board): the ID
			// is derived from the tasks on disk, so two unlocked adds read the
			// same max and mint the SAME id - O_EXCL only protects the file
			// NAME, and two different titles produce two different names, so
			// both writes succeed and the duplicate becomes unaddressable
			// (findByExactID returns whichever ReadDir yields first).
			//
			// The lock lives here, at the caller, exactly like pm_add_task's -
			// it has to cover NextTaskID, which is outside AddTask, and
			// LockProject must never nest (the MCP handler already holds it
			// when it calls AddTask). Best-effort: a lock failure degrades to
			// the previous unlocked behaviour rather than blocking the add.
			if release, lockErr := store.LockProject(projectSlug); lockErr == nil {
				defer release()
			}

			title := args[1]
			t := storage.NewTask("", title, projectSlug)

			if status != "" {
				t.SetStatus(storage.ParseStatus(status))
				// SetStatus took its own clock read; realign the pair NewTask
				// wrote from one read, so a task born on a non-default status
				// never reports a status change later than its own creation.
				t.Meta.Updated = t.Meta.StatusChanged
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
			t.Meta.Order = order

			if err := store.AddTask(projectSlug, t); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Created task: %s → %s\n", t.Meta.ID, t.FilePath)
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "todo", "Task status (todo, doing, done)")
	cmd.Flags().String("id", "", "Task ID (auto-generated if not set)")
	cmd.Flags().StringToStringVarP(&links, "link", "l", nil, "Links (key=url, repeatable: -l azure=https://...)")
	cmd.Flags().StringVar(&branch, "branch", "", "Git branch name")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags")
	cmd.Flags().IntVar(&order, "order", 0, "Sort order within column/rollup (lower first; convention 10, 20, 30...)")

	return cmd
}
