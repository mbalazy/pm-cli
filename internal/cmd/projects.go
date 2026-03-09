package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/spf13/cobra"
)

func newProjectsCmd(store storage.TaskStore) *cobra.Command {
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
				line := fmt.Sprintf("  %-20s %s  [%s]", slug, p.Name, strings.Join(parts, " "))
				if p.Stack != "" {
					line += fmt.Sprintf("  (%s)", p.Stack)
				}
				fmt.Println(line)
			}
			return nil
		},
	}

	cmd.AddCommand(newProjectsAddCmd(store))
	cmd.AddCommand(newProjectsEditCmd(store))
	return cmd
}

func newProjectsEditCmd(store storage.TaskStore) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <slug>",
		Short: "Open project.yaml in $EDITOR",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, err := store.ResolveProject(args[0])
			if err != nil {
				return err
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "nvim"
			}
			c := exec.Command(editor, store.ProjectYAML(slug))
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
}

func newProjectsAddCmd(store storage.TaskStore) *cobra.Command {
	var repoPath, repoURL, displayName, stack, notes string
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
				Name:  name,
				Path:  repoPath,
				Repo:  repoURL,
				Stack: stack,
				Tags:  tags,
				Notes: notes,
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
	cmd.Flags().StringVar(&stack, "stack", "", "Tech stack summary")
	cmd.Flags().StringVar(&notes, "notes", "", "Project notes (client, schedule, etc.)")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tags")

	return cmd
}
