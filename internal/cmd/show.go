package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/mbalazy/pm-cli/internal/storage"
	"github.com/spf13/cobra"
)

func newShowCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "show <project> <task-id>",
		Short: "Show task details",
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

			// render header
			var header strings.Builder
			fmt.Fprintf(&header, "# %s", task.Meta.Title)
			if task.Meta.ID != "" {
				fmt.Fprintf(&header, " (#%s)", task.Meta.ID)
			}
			fmt.Fprintln(&header)
			fmt.Fprintf(&header, "\n**Status:** %s | **Project:** %s | **Updated:** %s\n", task.Meta.Status, task.Project, storage.StampDate(task.Meta.Updated))

			// Both are omitted when unset: status_changed is empty on every
			// task written before the field existed, and a blocker nobody
			// recorded is not a blocker worth a blank line.
			if task.Meta.StatusChanged != "" {
				fmt.Fprintf(&header, "\n**Status since:** %s\n", storage.StampDate(task.Meta.StatusChanged))
			}

			if task.Meta.WaitingFor != "" {
				fmt.Fprintf(&header, "\n**Waiting for:** %s\n", task.Meta.WaitingFor)
			}

			if task.Meta.Branch != "" {
				fmt.Fprintf(&header, "\n**Branch:** `%s`\n", task.Meta.Branch)
			}

			// links
			if len(task.Meta.Links) > 0 {
				var linkParts []string
				for name, url := range task.Meta.Links {
					linkParts = append(linkParts, fmt.Sprintf("[%s](%s)", name, url))
				}
				fmt.Fprintf(&header, "\n**Links:** %s\n", strings.Join(linkParts, " | "))
			}

			if len(task.Meta.Tags) > 0 {
				fmt.Fprintf(&header, "\n**Tags:** %s\n", strings.Join(task.Meta.Tags, ", "))
			}

			content := header.String()
			if task.Body != "" {
				content += "\n---\n\n" + task.Body
			}

			rendered, err := glamour.Render(content, "dark")
			if err != nil {
				// fallback to raw
				fmt.Println(content)
				return nil
			}
			fmt.Print(rendered)
			return nil
		},
	}
}
