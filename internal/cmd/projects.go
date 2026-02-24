package cmd

import (
	"fmt"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newProjectsCmd(store *storage.Store) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"proj"},
		Short:   "List or manage projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := store.ListProjects()
			if err != nil {
				return err
			}
			if len(projects) == 0 {
				fmt.Println("No projects. Use: pm projects add <name>")
				return nil
			}
			for _, slug := range projects {
				p, err := store.GetProject(slug)
				if err != nil {
					fmt.Printf("  %s (error reading)\n", slug)
					continue
				}
				tasks, _ := store.GetTasks(slug)
				counts := map[storage.TaskStatus]int{}
				for _, t := range tasks {
					counts[t.Meta.Status]++
				}
				statuses := p.GetStatuses()
				var parts []string
				for _, s := range statuses {
					parts = append(parts, fmt.Sprintf("%s:%d", s, counts[s]))
				}
				fmt.Printf("  %-20s %s  [%s]\n", slug, p.Name, strings.Join(parts, " "))
			}
			return nil
		},
	}

	cmd.AddCommand(newProjectsAddCmd(store))
	return cmd
}

func newProjectsAddCmd(store *storage.Store) *cobra.Command {
	var repoPath, repoURL, displayName string
	var tags []string

	cmd := &cobra.Command{
		Use:   "add <slug>",
		Short: "Add a new project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug := args[0]
			name := slug
			if displayName != "" {
				name = displayName
			}

			p := &storage.Project{
				Name: name,
				Path: repoPath,
				Repo: repoURL,
				Tags: tags,
			}

			if err := store.CreateProject(slug, p); err != nil {
				return err
			}
			fmt.Printf("Created project: %s (%s)\n", slug, store.ProjectDir(slug))
			return nil
		},
	}

	cmd.Flags().StringVar(&repoPath, "path", "", "Local repo path")
	cmd.Flags().StringVar(&repoURL, "repo", "", "Remote repo URL")
	cmd.Flags().StringVar(&displayName, "name", "", "Display name (defaults to slug)")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags")

	return cmd
}
