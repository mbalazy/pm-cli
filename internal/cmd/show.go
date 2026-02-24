package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newShowCmd(store *storage.Store) *cobra.Command {
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
			fmt.Fprintf(&header, "\n**Status:** %s | **Project:** %s | **Updated:** %s\n", task.Meta.Status, task.Project, task.Meta.Updated)

			if task.Meta.Branch != "" {
				fmt.Fprintf(&header, "\n**Branch:** `%s`\n", task.Meta.Branch)
			}

			// links
			links := []string{}
			if task.Meta.Links.Azure != "" {
				links = append(links, fmt.Sprintf("[Azure DevOps](%s)", task.Meta.Links.Azure))
			}
			if task.Meta.Links.Jira != "" {
				links = append(links, fmt.Sprintf("[Jira](%s)", task.Meta.Links.Jira))
			}
			if task.Meta.Links.Figma != "" {
				links = append(links, fmt.Sprintf("[Figma](%s)", task.Meta.Links.Figma))
			}
			if len(links) > 0 {
				fmt.Fprintf(&header, "\n**Links:** %s\n", strings.Join(links, " | "))
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
