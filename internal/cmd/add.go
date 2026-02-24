package cmd

import (
	"fmt"
	"time"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newAddCmd(store *storage.Store) *cobra.Command {
	var status, linkAzure, linkJira, linkFigma, branch string
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

			if id, _ := cmd.Flags().GetString("id"); id != "" {
				t.Meta.ID = id
			} else {
				// auto-generate ID from timestamp
				t.Meta.ID = fmt.Sprintf("%d", time.Now().Unix()%100000)
			}

			t.Meta.Links = storage.Links{
				Azure: linkAzure,
				Jira:  linkJira,
				Figma: linkFigma,
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
	cmd.Flags().StringVar(&linkAzure, "link-azure", "", "Azure DevOps link")
	cmd.Flags().StringVar(&linkJira, "link-jira", "", "Jira link")
	cmd.Flags().StringVar(&linkFigma, "link-figma", "", "Figma link")
	cmd.Flags().StringVar(&branch, "branch", "", "Git branch name")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags")

	return cmd
}
