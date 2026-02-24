package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newListCmd(store *storage.Store) *cobra.Command {
	var project, status string

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			var tasks []*storage.Task
			var err error

			if project != "" {
				slug, err2 := store.ResolveProject(project)
				if err2 != nil {
					return err2
				}
				tasks, err = store.GetTasks(slug)
			} else {
				tasks, err = store.GetAllTasks()
			}
			if err != nil {
				return err
			}

			// filter by status
			if status != "" {
				s := storage.ParseStatus(status)
				var filtered []*storage.Task
				for _, t := range tasks {
					if t.Meta.Status == s {
						filtered = append(filtered, t)
					}
				}
				tasks = filtered
			}

			if len(tasks) == 0 {
				fmt.Println("No tasks found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "STATUS\tPROJECT\tID\tTITLE\n")
			fmt.Fprintf(w, "------\t-------\t--\t-----\n")
			for _, t := range tasks {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					statusIcon(t.Meta.Status),
					t.Project,
					t.Meta.ID,
					t.Meta.Title,
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Filter by project")
	cmd.Flags().StringVarP(&status, "status", "s", "", "Filter by status (todo, doing, done)")

	return cmd
}

func statusIcon(s storage.TaskStatus) string {
	switch s {
	case storage.StatusTodo:
		return "[ ] todo"
	case storage.StatusDoing:
		return "[~] doing"
	case storage.StatusDone:
		return "[x] done"
	default:
		return fmt.Sprintf("[?] %s", s)
	}
}
